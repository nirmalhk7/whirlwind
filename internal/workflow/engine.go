package workflow

import (
	"context"
	"log"
	"time"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// New constructs the workflow engine with default process adapters, a unique lease owner, and execution tracking.
func New(s *store.Store, g github.Client, root string) *Engine {
	return &Engine{Store: s, GitHub: g, Root: root, Adapter: func(p model.Provider) provider.Adapter { return provider.ProcessAdapter{Provider: p} }, owner: store.ID(), running: map[string]context.CancelFunc{}, active: map[string]int{}, wake: make(chan struct{}, 1)}
}

// Wake is a coalesced hint. Durable state remains the source of dispatch eligibility.
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) Owner() string { return e.owner }

// Run dispatches task steps, schedules daily ideation and reconciliation, and cancels and waits for background work on shutdown.
func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastSettingsError := ""
	nextReconcile := time.Now().Add(5 * time.Minute)
	if err := e.reconcileStandaloneRuntimes(ctx); err != nil {
		log.Printf("reconcile standalone sandbox runtimes: %v", err)
	}
	if !e.settings(ctx).Stopped {
		e.background.Go(func() { e.Reconcile(ctx) })
	}
	lastSettingsError, nextReconcile = e.runCycle(ctx, lastSettingsError, nextReconcile)
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
		case <-e.wake:
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
		log.Printf("standalone sandbox runtime recovery blocks dispatch: %v", err)
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

// Project loads and decodes the registered project used by a task.
func (e *Engine) Project(ctx context.Context, id string) (model.Project, error) {
	r, err := e.Store.Get(ctx, "projects", id)
	if err != nil {
		return model.Project{}, err
	}
	return store.Decode[model.Project](r)
}
