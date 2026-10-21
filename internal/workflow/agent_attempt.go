package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type agentAttempt struct {
	*agentRun
	chosen   model.Provider
	id       string
	state    model.Attempt
	mu       sync.Mutex
	eventErr error
	cancel   context.CancelFunc
}

func (a *agentAttempt) execute() (provider.Output, error) {
	a.mu.Lock()
	priorErr := a.eventErr
	a.mu.Unlock()
	if priorErr != nil {
		return provider.Output{}, priorErr
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.cancel = cancel
	a.mu.Unlock()
	defer cancel()
	hooks := provider.Hooks{Questions: a.questions, Event: a.event, Permission: a.permission}
	input := a.input
	input.Policy = a.project.Policy
	started := time.Now()
	output, err := a.Adapter(provider.ExecutionProvider(a.chosen, input.Thinking)).Run(ctx, input, hooks)
	if a.input.Purpose == "provider_test" && err == nil && strings.TrimSpace(output.Text) == "" {
		err = errors.New("provider completed without a reply; check agent setup and retry")
	}
	output.AttemptID = a.id
	a.mu.Lock()
	err = errors.Join(err, a.eventErr)
	a.mu.Unlock()
	raw := output.Text
	output.Text = a.finishLearning(context.WithoutCancel(ctx), raw, err)
	evidenceOutput := output
	evidenceOutput.Text = raw
	if finishErr := a.finish(evidenceOutput, err, started); finishErr != nil {
		return output, errors.Join(err, fmt.Errorf("%w: %v", store.ErrPersistence, finishErr))
	}
	return output, err
}
func (a *agentAttempt) finish(output provider.Output, runErr error, started time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	final, err := a.Store.Capture(ctx, a.id, "final_output", "text/plain", []byte(output.Text), store.MessagePayloadLimit, false)
	if err != nil {
		return err
	}
	record, err := a.Store.Get(ctx, "sessions", a.id)
	if err != nil {
		return err
	}
	if output.SessionID != "" {
		a.state.SessionID = output.SessionID
	}
	a.state.Finished = true
	a.state.Status = "complete"
	a.state.OutputArtifact = final.ID
	a.state.Summary = string(store.Prefix([]byte(output.Text), 4096))
	a.state.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	elapsed := time.Since(started).Milliseconds()
	a.state.ElapsedMS = &elapsed
	success := runErr == nil
	a.state.Success = &success
	if output.ResolvedModel != "" {
		a.state.ResolvedModel = output.ResolvedModel
	}
	if output.EffectiveThinking != nil {
		a.state.EffectiveThinking = output.EffectiveThinking
	}
	if runErr != nil {
		a.state.Error = runErr.Error()
		a.state.Status = "failed"
		if errors.Is(runErr, context.Canceled) {
			a.state.Status = "cancelled"
		}
	}
	a.state.Usage = output.Usage
	tokens, cost := output.Tokens, output.Cost
	if output.Usage != nil && output.Usage.Scope == "session" {
		tokens = nil
		cost = nil
	}
	metric := map[string]any{"attempt_id": a.id, "project_id": a.project.ID, "task": a.task.ID, "unit_id": a.input.UnitID, "operation_id": a.input.OperationID, "role": a.input.Role, "provider": a.chosen.ID, "model": a.input.Model, "resolved_model": output.ResolvedModel, "tokens": tokens, "cost": cost, "usage": output.Usage, "elapsed_ms": time.Since(started).Milliseconds(), "success": runErr == nil, "review_attempts": a.task.ReviewPasses, "category": a.index.Category(), "risk": a.task.Risk}
	return a.Store.Write(ctx, []store.Change{{Kind: "sessions", ID: a.id, Value: a.state, Expected: record.Version}, {Kind: "metrics", ID: a.id, Value: metric}}, nil)
}
