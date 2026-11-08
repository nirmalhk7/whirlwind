package workflow

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type Engine struct {
	Store                    *store.Store
	GitHub                   github.Client
	Root                     string
	PlansRoot                string
	Adapter                  func(model.Provider) provider.Adapter
	owner                    string
	mu                       sync.Mutex
	runners                  map[string]context.CancelFunc
	projectRunners           map[string]projectRunner
	running                  map[string]context.CancelFunc
	active                   map[string]int
	gitMu                    sync.Mutex
	background               sync.WaitGroup
	runnerWait               sync.WaitGroup
	closing                  bool
	providerStatuses         providerStatusCache
	retryWait                func(context.Context, time.Duration) error
	attemptTimeout           time.Duration
	integrationBrokerFactory func(context.Context, model.Project, []model.IntegrationSource) (*integrationMCPBroker, error)
	integrationPromptSource  func(string) (string, error)
}

// New constructs the workflow engine with default process adapters, a unique lease owner, and execution tracking.
func New(s *store.Store, g github.Client, root string) *Engine {
	return &Engine{Store: s, GitHub: g, Root: root, Adapter: func(p model.Provider) provider.Adapter { return provider.ProcessAdapter{Provider: p} }, owner: store.ID(), running: map[string]context.CancelFunc{}, active: map[string]int{}}
}

func (e *Engine) Owner() string { return e.owner }

// Run dispatches task steps, schedules daily ideation and reconciliation, and cancels and waits for background work on shutdown.
func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastSettingsError := ""
	nextReconcile := time.Now().Add(5 * time.Minute)
	if err := e.reconcileStandaloneRuntimes(ctx); err != nil {
		log.Printf("reconcile standalone OpenShell runtimes: %v", err)
	}
	if !e.settings(ctx).Stopped {
		e.background.Go(func() { e.Reconcile(ctx) })
	}
	for {
		select {
		case <-ctx.Done():
			e.stopRunners()
			e.runnerWait.Wait()
			e.background.Wait()
			e.mu.Lock()
			e.closing = false
			e.mu.Unlock()
			return
		case <-ticker.C:
			lastSettingsError, nextReconcile = e.runCycle(ctx, lastSettingsError, nextReconcile)
		}
	}
}

func (e *Engine) runCycle(ctx context.Context, lastSettingsError string, nextReconcile time.Time) (string, time.Time) {
	if err := e.Store.RefreshSettings(ctx); err != nil {
		if err.Error() != lastSettingsError {
			log.Printf("settings configuration: %v", err)
			lastSettingsError = err.Error()
		}
		return lastSettingsError, nextReconcile
	}
	if e.settings(ctx).Stopped {
		e.StopAll()
		return "", nextReconcile
	}
	if err := e.reconcileStandaloneRuntimes(ctx); err != nil {
		log.Printf("standalone OpenShell runtime recovery blocks dispatch: %v", err)
		return lastSettingsError, nextReconcile
	}
	e.refreshLocalApprovals(ctx)
	e.refreshQuotaWaits(ctx)
	e.dispatch(ctx)
	if !time.Now().Before(nextReconcile) {
		e.background.Go(func() { e.Reconcile(ctx) })
		settings := e.settings(ctx)
		nextReconcile = time.Now().Add(time.Duration(settings.ReconciliationSeconds) * time.Second)
	}
	e.background.Go(func() { e.Daily(ctx) })
	return "", nextReconcile
}

// Stop cancels an active task step so its agent processes can terminate.
func (e *Engine) Stop(id string) {
	e.mu.Lock()
	if c := e.running[id]; c != nil {
		c()
	}
	e.mu.Unlock()
}

// fail pauses a failed task at its resumable stage and creates an Inbox recovery notification.
func (e *Engine) fail(ctx context.Context, id string, failure error) {
	if err := e.pauseFailure(ctx, id, failure); err != nil && !errors.Is(err, store.ErrConflict) {
		log.Printf("persist task recovery %s: %v", id, err)
	}
}

func (e *Engine) pauseFailure(ctx context.Context, id string, failure error) error {
	r, err := e.Store.Get(ctx, "tasks", id)
	if err != nil {
		return err
	}
	task, err := store.Decode[model.Task](r)
	if err != nil || task.Stage == "paused" || task.Stage == "plan_approval" || task.Cancelled() {
		return err
	}

	var wait *AdmissionWait
	if errors.As(failure, &wait) {
		if task.Stage == "waiting_quota" {
			return nil
		}
		task.Wait = &model.TaskWait{Kind: wait.Kind, Reason: wait.Reason, Provider: wait.Provider, Pool: wait.Pool, Role: wait.Role, ResumeStage: task.Stage, NextCheckAt: wait.NextCheck.UTC().Format(time.RFC3339Nano), PlanVersion: task.PlanVersion}
		if project, err := e.Project(ctx, task.ProjectID); err == nil {
			task.Wait.PolicyHash = project.PolicyHash
		}
		task.ResumeStage, task.Stage, task.Error = task.Stage, "waiting_quota", failure.Error()
		if wait.Kind == "setup" || wait.Kind == "policy" || wait.Kind == "authentication" {
			task.Stage = "paused"
		}
	} else {
		task.ResumeStage, task.Stage, task.Error = task.Stage, "paused", failure.Error()
	}
	// Store.Put commits the structured transition notification in the same transaction.
	return e.Store.Put(ctx, "tasks", id, task, r.Version)
}

// Project loads and decodes the registered project used by a task.
func (e *Engine) Project(ctx context.Context, id string) (model.Project, error) {
	r, err := e.Store.Get(ctx, "projects", id)
	if err != nil {
		return model.Project{}, err
	}
	return store.Decode[model.Project](r)
}
