package workflow

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"time"
	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type agentRun struct {
	*Engine
	ctx             context.Context
	project         model.Project
	task            model.Task
	input           provider.Input
	providers       []model.Provider
	index           ctxengine.Index
	resumeSource    string
	previousAttempt string
	fallbackReason  string
}

// agent reserves capacity for a named request, prepares its context, and retries unavailable providers.
func (e *Engine) agent(ctx context.Context, p model.Project, t model.Task, input provider.Input) (provider.Output, error) {
	if input.OperationID == "" {
		input.OperationID = operationID(ctx)
	}
	run := &agentRun{Engine: e, ctx: ctx, project: p, task: t, input: input}
	return run.run()
}
func (a *agentRun) run() (provider.Output, error) {
	if a.project.Policy.RunTimeoutSeconds > 0 {
		ctx, cancel := context.WithTimeout(a.ctx, time.Duration(a.project.Policy.RunTimeoutSeconds)*time.Second)
		defer cancel()
		a.ctx = ctx
	}
	cwd, err := filepath.EvalSymlinks(a.input.CWD)
	if err != nil {
		return provider.Output{}, err
	}
	a.input.CWD = cwd
	if err = a.acquire(a.ctx, a.project); err != nil {
		return provider.Output{}, err
	}
	defer a.release(a.project.ID)
	if err = a.prepare(); err != nil {
		return provider.Output{}, err
	}
	return a.route()
}
func (a *agentRun) route() (provider.Output, error) {
	record, err := a.Store.Get(a.ctx, "agents", a.input.Role)
	if err == nil {
		config, err := store.Decode[model.AgentConfig](record)
		if err != nil {
			return provider.Output{}, err
		}
		return a.routeConfigured(config)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return provider.Output{}, err
	}
	return a.routeAvailable()
}

func (a *agentRun) routeAvailable() (provider.Output, error) {
	remaining := slices.Clone(a.providers)
	choices := []model.AgentChoice{}
	preferences := a.preferences()
	outcomes := a.outcomes(a.ctx, a.input.Role, a.index.Category())
	for len(remaining) > 0 {
		chosen, err := provider.Route(remaining, a.input.Role, preferences, outcomes)
		if err != nil {
			break
		}
		choices = append(choices, model.AgentChoice{Provider: chosen.ID, Model: a.input.Model, Thinking: a.input.Thinking})
		remaining = slices.DeleteFunc(remaining, func(p model.Provider) bool { return p.ID == chosen.ID })
	}
	for _, source := range remaining {
		if slices.Contains(source.Capabilities, a.input.Role) {
			choices = append(choices, model.AgentChoice{Provider: source.ID, Model: a.input.Model, Thinking: a.input.Thinking})
		}
	}
	return a.routeChoices(choices)
}
func (a *agentRun) preferences() []string {
	preferred := append([]string{}, a.project.Policy.Providers...)
	if a.task.Recoveries == 0 {
		return preferred
	}
	candidates := []string{}
	for _, candidate := range a.providers {
		if candidate.Available {
			candidates = append(candidates, candidate.ID)
		}
	}
	if len(candidates) > 0 {
		offset := a.task.Recoveries % len(candidates)
		preferred = append(candidates[offset:], candidates[:offset]...)
	}
	return preferred
}

type operationContextKey struct{}

func WithOperation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, operationContextKey{}, id)
}
func operationID(ctx context.Context) string {
	id, _ := ctx.Value(operationContextKey{}).(string)
	return id
}
