package workflow

import (
	"context"
	"log"
	"sync"
	"time"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type Engine struct {
	Store      *store.Store
	GitHub     github.Client
	Root       string
	Adapter    func(model.Provider) provider.Adapter
	owner      string
	mu         sync.Mutex
	running    map[string]context.CancelFunc
	active     map[string]int
	gitMu      sync.Mutex
	background sync.WaitGroup
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
	e.background.Go(func() { e.Reconcile(ctx) })
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
			if err := e.Store.RefreshSettings(ctx); err != nil {
				if err.Error() != lastSettingsError {
					log.Printf("settings configuration: %v", err)
					lastSettingsError = err.Error()
				}
				continue
			}
			lastSettingsError = ""
			e.dispatch(ctx)
			if !time.Now().Before(nextReconcile) {
				e.background.Go(func() { e.Reconcile(ctx) })
				settings := e.settings(ctx)
				nextReconcile = time.Now().Add(time.Duration(settings.ReconciliationSeconds) * time.Second)
			}
			e.background.Go(func() { e.Daily(ctx) })
		}
	}
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
func (e *Engine) fail(ctx context.Context, id string, err error) {
	r, x := e.Store.Get(ctx, "tasks", id)
	if x != nil {
		return
	}
	t, x := store.Decode[model.Task](r)
	if x != nil {
		return
	}
	if t.Stage != "paused" {
		t.ResumeStage = t.Stage
	}
	t.Stage = "paused"
	t.Error = err.Error()
	if e.Store.Put(ctx, "tasks", id, t, r.Version) == nil {
		_ = e.Store.Notify(ctx, id, "recovery", err.Error())
	}
}

// Project loads and decodes the registered project used by a task.
func (e *Engine) Project(ctx context.Context, id string) (model.Project, error) {
	r, err := e.Store.Get(ctx, "projects", id)
	if err != nil {
		return model.Project{}, err
	}
	return store.Decode[model.Project](r)
}
