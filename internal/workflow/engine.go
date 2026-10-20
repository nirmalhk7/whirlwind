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
	Store            *store.Store
	GitHub           github.Client
	Root             string
	Adapter          func(model.Provider) provider.Adapter
	owner            string
	mu               sync.Mutex
	runners          map[string]context.CancelFunc
	running          map[string]context.CancelFunc
	active           map[string]int
	gitMu            sync.Mutex
	background       sync.WaitGroup
	providerStatuses providerStatusCache
	retryWait        func(context.Context, time.Duration) error
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
	if !e.settings(ctx).Stopped {
		e.background.Go(func() { e.Reconcile(ctx) })
	}
	for {
		select {
		case <-ctx.Done():
			e.mu.Lock()
			for _, cancel := range e.running {
				cancel()
			}
			e.mu.Unlock()
			e.background.Wait()
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
	if err != nil || task.Stage == "paused" {
		return err
	}
	task.ResumeStage, task.Stage, task.Error = task.Stage, "paused", failure.Error()
	notice := model.Notification{ID: store.ID(), TaskID: id, Kind: "recovery", Message: failure.Error()}
	return e.Store.Write(ctx, []store.Change{{Kind: "tasks", ID: id, Value: task, Expected: r.Version}, {Kind: "inbox", ID: notice.ID, Value: notice}}, nil)
}

// Project loads and decodes the registered project used by a task.
func (e *Engine) Project(ctx context.Context, id string) (model.Project, error) {
	r, err := e.Store.Get(ctx, "projects", id)
	if err != nil {
		return model.Project{}, err
	}
	return store.Decode[model.Project](r)
}
