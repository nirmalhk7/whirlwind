package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type agentRun struct {
	*Engine
	ctx                  context.Context
	project              model.Project
	task                 model.Task
	input                provider.Input
	providers            []model.Provider
	index                ctxengine.Index
	resumeSource         string
	previousAttempt      string
	fallbackReason       string
	recoveryRestartCount int
	queuePosition        int
	queueLength          int
}

// agent reserves capacity for a named request, prepares its context, and retries unavailable providers.
func (e *Engine) agent(ctx context.Context, p model.Project, t model.Task, input provider.Input) (provider.Output, error) {
	ctx, release, err := e.projectRunnerContext(ctx, p.ID)
	if err != nil {
		return provider.Output{}, err
	}
	defer release()
	if input.OperationID == "" {
		input.OperationID = operationID(ctx)
	}
	run := &agentRun{Engine: e, ctx: ctx, project: p, task: t, input: input}
	return run.run()
}
func (a *agentRun) run() (provider.Output, error) {
	cwd, err := filepath.EvalSymlinks(a.input.CWD)
	if err != nil {
		return provider.Output{}, err
	}
	a.input.CWD = cwd
	parent := a.ctx
	prepareCtx, cancel := context.WithTimeout(parent, preparationJobTimeout)
	a.ctx = prepareCtx
	err = a.prepare()
	cancel()
	a.ctx = parent
	if err != nil {
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
	if slices.Contains(model.AgentRoles(), a.input.Role) || a.input.Role == "submit" {
		return provider.Output{}, fmt.Errorf("no configured agent queue for role %q; configure its ordered provider/model/thinking entries", a.input.Role)
	}
	return a.routeAvailable()
}

func (a *agentRun) routeAvailable() (provider.Output, error) {
	remaining := slices.Clone(a.providers)
	choices := a.availableChoices(&remaining)
	return a.routeChoices(choices)
}

func (a *agentRun) availableChoices(remaining *[]model.Provider) []model.AgentChoice {
	preferences := a.preferences()
	outcomes := map[string]float64{}
	if a.project.Policy.AdaptiveRouting {
		outcomes = a.outcomes(a.ctx, a.input.Role, a.index.Category())
	}
	if a.input.Role == "submit" {
		grantSubmitCapability(*remaining)
	}
	choices, leftover := preferredChoices(*remaining, a.input.Role, a.input.Model, a.input.Thinking, preferences)
	choices = append(choices, rankedChoices(&leftover, a.input.Role, a.input.Model, a.input.Thinking, preferences, outcomes)...)
	return append(choices, unrankedChoices(leftover, a.input.Role, a.input.Model, a.input.Thinking)...)
}

func grantSubmitCapability(remaining []model.Provider) {
	for i := range remaining {
		if slices.Contains(remaining[i].Capabilities, "code") && !slices.Contains(remaining[i].Capabilities, "submit") {
			remaining[i].Capabilities = append(slices.Clone(remaining[i].Capabilities), "submit")
		}
	}
}

func preferredChoices(remaining []model.Provider, role, modelName string, thinking *model.ThinkingChoice, preferences []string) ([]model.AgentChoice, []model.Provider) {
	choices := []model.AgentChoice{}
	// Explicit preferences are authoritative even when measured rankings differ.
	for _, id := range preferences {
		for _, candidate := range remaining {
			if candidate.ID == id && slices.Contains(candidate.Capabilities, role) {
				choices = append(choices, model.AgentChoice{Provider: id, Model: modelName, Thinking: thinking})
			}
		}
		remaining = slices.DeleteFunc(remaining, func(p model.Provider) bool { return p.ID == id })
	}
	return choices, remaining
}

func rankedChoices(remaining *[]model.Provider, role, modelName string, thinking *model.ThinkingChoice, preferences []string, outcomes map[string]float64) []model.AgentChoice {
	choices := []model.AgentChoice{}
	for len(*remaining) > 0 {
		chosen, err := provider.Route(*remaining, role, preferences, outcomes)
		if err != nil {
			break
		}
		choices = append(choices, model.AgentChoice{Provider: chosen.ID, Model: modelName, Thinking: thinking})
		*remaining = slices.DeleteFunc(*remaining, func(p model.Provider) bool { return p.ID == chosen.ID })
	}
	return choices
}

func unrankedChoices(remaining []model.Provider, role, modelName string, thinking *model.ThinkingChoice) []model.AgentChoice {
	choices := []model.AgentChoice{}
	for _, source := range remaining {
		if slices.Contains(source.Capabilities, role) {
			choices = append(choices, model.AgentChoice{Provider: source.ID, Model: modelName, Thinking: thinking})
		}
	}
	return choices
}
func (a *agentRun) preferences() []string {
	return append([]string{}, a.project.Policy.Providers...)
}

type operationContextKey struct{}

func WithOperation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, operationContextKey{}, id)
}
func operationID(ctx context.Context) string {
	id, _ := ctx.Value(operationContextKey{}).(string)
	return id
}
