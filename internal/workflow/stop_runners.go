package workflow

import (
	"context"
	"errors"
	"whirlwind/internal/store"
)

var ErrRunnersStopped = errors.New("runners are stopped; set workers.stopped to false in settings.yaml to allow new runs")

// runnerContext tracks the entire operation, including waits for capacity and retries.
func (e *Engine) runnerContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	id := store.ID()
	e.mu.Lock()
	if e.runners == nil {
		e.runners = map[string]context.CancelFunc{}
	}
	e.runners[id] = cancel
	e.mu.Unlock()
	if e.settings(parent).Stopped {
		cancel()
	}
	return ctx, func() {
		cancel()
		e.mu.Lock()
		delete(e.runners, id)
		e.mu.Unlock()
	}
}

// StopAll cancels current runners. The persisted workers.stopped flag controls new work.
func (e *Engine) StopAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, cancel := range e.running {
		cancel()
	}
	for _, cancel := range e.runners {
		cancel()
	}
}
