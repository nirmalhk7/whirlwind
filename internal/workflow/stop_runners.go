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
	if e.closing {
		e.mu.Unlock()
		cancel()
		return ctx, cancel
	}
	e.runners[id] = cancel
	e.runnerWait.Add(1)
	e.mu.Unlock()
	if e.settings(parent).Stopped {
		cancel()
	}
	return ctx, func() {
		cancel()
		e.mu.Lock()
		delete(e.runners, id)
		e.mu.Unlock()
		e.runnerWait.Done()
	}
}

func (e *Engine) stopRunners() {
	e.mu.Lock()
	e.closing = true
	for _, cancel := range e.running {
		cancel()
	}
	for _, cancel := range e.runners {
		cancel()
	}
	e.mu.Unlock()
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
