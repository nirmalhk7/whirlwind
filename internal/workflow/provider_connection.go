package workflow

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

// TestProviderConnection sends one harmless prompt using the configured provider and normal capacity limits.
func (e *Engine) TestProviderConnection(ctx context.Context, p model.Provider, models ...string) (provider.Output, error) {
	selectedModel := ""
	if len(models) > 0 {
		selectedModel = models[0]
	}
	return e.TestProviderChoice(ctx, p, model.AgentChoice{Model: selectedModel})
}

func (e *Engine) TestProviderChoice(ctx context.Context, p model.Provider, choice model.AgentChoice) (provider.Output, error) {
	ctx, release := e.runnerContext(ctx)
	defer release()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	capacity := model.Project{ID: "provider-test:" + p.ID}
	if err := e.acquire(ctx, capacity); err != nil {
		return provider.Output{}, err
	}
	defer e.release(capacity.ID)
	workspace, err := os.MkdirTemp("", "whirlwind-connectivity-")
	if err != nil {
		return provider.Output{}, err
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	input := provider.Input{Model: choice.Model, Thinking: choice.Thinking, Role: "plan", Purpose: "provider_test", CWD: workspace, ReadOnly: true, Prompt: "Reply with exactly Hello World. Do not use tools or inspect or modify files."}
	var out provider.Output
	if id := operationID(ctx); id != "" {
		input.OperationID = id
		run := &agentRun{Engine: e, ctx: ctx, project: model.Project{}, task: model.Task{ID: "operation:" + id}, input: input}
		attempt, attemptErr := run.newAttempt(p)
		if attemptErr != nil {
			return out, attemptErr
		}
		out, err = attempt.execute()
	} else {
		out, err = e.Adapter(provider.ExecutionProvider(p, choice.Thinking)).Run(ctx, input, provider.Hooks{})
	}

	if err == nil && strings.TrimSpace(out.Text) == "" {
		err = errors.New("provider completed without a reply; check agent setup and retry")
	}
	return out, err
}
