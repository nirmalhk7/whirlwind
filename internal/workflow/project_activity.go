package workflow

import (
	"context"
	"errors"
	"whirlwind/internal/store"
)

var ErrProjectPaused = errors.New("project is paused; resume project activity before starting new runs")

type projectRunner struct {
	project string
	cancel  context.CancelFunc
}

// projectRunnerContext tracks the whole operation, including capacity waits and
// retries. Register before reading persisted state so a concurrent pause either
// cancels this runner or is observed by the state check.
func (e *Engine) projectRunnerContext(parent context.Context, project string) (context.Context, func(), error) {
	ctx, release := e.runnerContext(parent)
	id := store.ID()
	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	if e.projectRunners == nil {
		e.projectRunners = map[string]projectRunner{}
	}
	e.projectRunners[id] = projectRunner{project, cancel}
	e.mu.Unlock()
	cleanup := func() {
		cancel()
		e.mu.Lock()
		delete(e.projectRunners, id)
		e.mu.Unlock()
		release()
	}
	p, err := e.Project(ctx, project)
	if err == nil && p.Paused {
		err = ErrProjectPaused
	}
	if err != nil {
		cleanup()
	}
	return ctx, cleanup, err
}

// StopProject cancels current project operations after its pause is committed.
// The persisted project flag prevents future work, including after a restart.
func (e *Engine) StopProject(project string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, runner := range e.projectRunners {
		if runner.project == project {
			runner.cancel()
		}
	}
}
