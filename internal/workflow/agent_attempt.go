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
	chosen      model.Provider
	id          string
	state       model.Attempt
	mu          sync.Mutex
	checkMu     sync.Mutex
	eventErr    error
	cancel      context.CancelFunc
	budgetClock *runtimeBudget
}

func (a *agentAttempt) execute() (provider.Output, error) {
	a.mu.Lock()
	priorErr := a.eventErr
	a.mu.Unlock()
	if priorErr != nil {
		return provider.Output{}, priorErr
	}
	ctx, cancel, budgetErr := a.activeBudgetContext(a.ctx)
	if budgetErr != nil {
		return provider.Output{}, budgetErr
	}
	a.mu.Lock()
	a.cancel = cancel
	a.mu.Unlock()
	defer cancel()
	hooks := provider.Hooks{Questions: a.questions, Event: a.event, Permission: a.permission}
	hooks.RunCheck = a.runCheck
	if a.budgetClock != nil {
		hooks.Questions = func(ctx context.Context, questions []model.Question) (map[string]string, error) {
			resume := a.budgetClock.pause()
			defer resume()
			return a.questions(ctx, questions)
		}
		hooks.Permission = func(ctx context.Context, permission provider.Permission) (bool, error) {
			resume := a.budgetClock.pause()
			defer resume()
			return a.permission(ctx, permission)
		}
	}
	input := a.input
	if input.DiscoverConnected {
		input.Prompt += "\nScope overrides for this provider only (unlisted connected sources remain discoverable):\n" + jsonText(input.IntegrationScopes)
	}
	if input.Purpose == "provider_test" {
		hooks.Permission = nil
		hooks.Questions = nil
		hooks.RunCheck = nil
	}
	input.Policy = a.project.Policy
	started := time.Now()
	output, err := a.Adapter(provider.ExecutionProvider(a.chosen, input.Thinking)).Run(ctx, input, hooks)
	if a.budgetClock != nil && a.budgetClock.exhausted() && a.ctx.Err() == nil {
		err = fmt.Errorf("%w: active runtime ceiling reached", store.ErrBudgetExceeded)
	}
	if a.input.Purpose == "provider_test" && err == nil && strings.TrimSpace(output.Text) == "" {
		err = errors.New("provider completed without a reply; check agent setup and retry")
	}
	output.AttemptID = a.id
	a.mu.Lock()
	err = errors.Join(err, a.eventErr)
	a.mu.Unlock()
	if finishErr := a.finish(output, err, started); finishErr != nil {
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
	activeElapsed := elapsed
	if a.budgetClock != nil {
		a.budgetClock.stop()
		activeElapsed = a.budgetClock.elapsed().Milliseconds()
	}
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
	if output.Usage == nil {
		output.Usage = a.state.Usage
	}
	a.state.Usage = output.Usage
	tokens, cost := output.Tokens, output.Cost
	if output.Usage != nil && output.Usage.Scope == "session" {
		tokens = nil
		cost = nil
	}
	metric := map[string]any{"attempt_id": a.id, "project_id": a.project.ID, "task": a.task.ID, "unit_id": a.input.UnitID, "operation_id": a.input.OperationID, "role": a.input.Role, "provider": a.chosen.ID, "model": a.input.Model, "resolved_model": output.ResolvedModel, "tokens": tokens, "cost": cost, "usage": output.Usage, "elapsed_ms": time.Since(started).Milliseconds(), "success": runErr == nil, "review_attempts": a.task.ReviewPasses, "category": a.index.Category(), "risk": a.task.Risk}
	entry := model.UsageEntry{Transport: a.state.Transport, ExecutionProfile: a.state.ExecutionProfile, PlanVersion: a.task.PlanVersion, AttemptID: a.id, TaskID: a.task.ID, ProjectID: a.project.ID, OperationID: a.input.OperationID, Provider: a.chosen.ID, Model: output.ResolvedModel, Role: a.input.Role, SessionID: output.SessionID, Resumed: a.state.RequestedSessionID != "", Usage: output.Usage, ElapsedMS: activeElapsed}
	if entry.Model == "" {
		entry.Model = a.input.Model
	}
	if entry.Usage == nil && (output.Tokens != nil || output.Cost != nil) {
		entry.Usage = &model.Usage{Scope: "attempt", TotalTokens: output.Tokens, Cost: output.Cost}
	}
	return a.Store.CompleteUsage(ctx, entry, []store.Change{{Kind: "sessions", ID: a.id, Value: a.state, Expected: record.Version}, {Kind: "metrics", ID: a.id, Value: metric}})
}
