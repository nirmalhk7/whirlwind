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
		if attemptDeadlineExpired(a.state, budgetErr) {
			a.state.ProcessState = "deadline_before_adapter"
			return a.finishExecution(provider.Output{}, budgetErr, persistedAttemptStart(a.state))
		}
		return provider.Output{}, budgetErr
	}
	a.mu.Lock()
	a.cancel = cancel
	a.mu.Unlock()
	defer cancel()
	if err := a.updateProcessState(ctx, "adapter_running"); err != nil {
		return provider.Output{}, a.persistenceError(err)
	}
	stopHeartbeat := a.startWorkerHeartbeat(ctx)
	hooks := a.executionHooks()
	input := a.input
	if input.DiscoverConnected {
		input.Prompt += "\nScope overrides for this provider only (unlisted connected sources remain discoverable):\n" + jsonText(input.IntegrationScopes)
	}
	input.Policy = a.project.Policy
	started := time.Now()
	output, err := a.Adapter(provider.ExecutionProvider(a.chosen, input.Thinking)).Run(ctx, input, hooks)
	stopHeartbeat()
	processCtx, processCancel := context.WithTimeout(context.Background(), 5*time.Second)
	processErr := a.updateProcessState(processCtx, "adapter_exited")
	processCancel()
	if processErr != nil {
		err = errors.Join(err, processErr)
	}
	if isolationErr := a.reconcileOwnedRuntime(); isolationErr != nil {
		err = errors.Join(err, isolationErr)
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	return a.finishExecution(output, err, started)
}

func persistedAttemptStart(state model.Attempt) time.Time {
	started, err := time.Parse(time.RFC3339Nano, state.StartedAt)
	if err == nil {
		return started
	}
	return time.Now()
}

func (a *agentAttempt) executionHooks() provider.Hooks {
	hooks := provider.Hooks{Questions: a.questions, Event: a.event, Permission: a.permission, RunCheck: a.runCheck}
	if a.budgetClock != nil {
		hooks.Questions = a.pausedQuestions
		hooks.Permission = a.pausedPermission
	}
	if a.input.Purpose == "provider_test" {
		hooks.Permission = nil
		hooks.Questions = nil
		hooks.RunCheck = nil
	}
	return hooks
}

func (a *agentAttempt) pausedQuestions(ctx context.Context, questions []model.Question) (map[string]string, error) {
	resume := a.budgetClock.pause()
	defer resume()
	return a.questions(ctx, questions)
}

func (a *agentAttempt) pausedPermission(ctx context.Context, permission provider.Permission) (bool, error) {
	resume := a.budgetClock.pause()
	defer resume()
	return a.permission(ctx, permission)
}

func (a *agentAttempt) finishExecution(output provider.Output, err error, started time.Time) (provider.Output, error) {
	err = attemptResultError(a, output, err)
	output.AttemptID = a.id
	a.mu.Lock()
	err = errors.Join(err, a.eventErr)
	a.mu.Unlock()
	if finishErr := a.finish(output, err, started); finishErr != nil {
		return output, errors.Join(err, fmt.Errorf("%w: %v", store.ErrPersistence, finishErr))
	}
	return output, err
}

func attemptResultError(attempt *agentAttempt, output provider.Output, err error) error {
	if activeRuntimeExceeded(attempt) {
		return fmt.Errorf("%w: active runtime ceiling reached", store.ErrBudgetExceeded)
	}
	if errors.Is(err, context.Canceled) && attempt.ctx.Err() == nil && attemptDeadlineNear(attempt.state) {
		return context.DeadlineExceeded
	}
	if providerTestReplyMissing(attempt, output, err) {
		return errors.New("provider completed without a reply; check agent setup and retry")
	}
	if err == nil {
		if validationErr := validateRoleOutput(attempt.input.Role, attempt.input.Purpose, output.Text); validationErr != nil {
			return fmt.Errorf("%w: %w", ErrMalformedRoleOutput, validationErr)
		}
	}
	return err
}

func activeRuntimeExceeded(attempt *agentAttempt) bool {
	if attempt.budgetClock == nil || !attempt.budgetClock.exhausted() || attempt.ctx.Err() != nil {
		return false
	}
	if attemptDeadlineNear(attempt.state) {
		return false
	}
	return true
}

func attemptDeadlineNear(state model.Attempt) bool {
	deadline, err := attemptDeadline(state)
	return err == nil && time.Until(deadline) <= 50*time.Millisecond
}

func providerTestReplyMissing(attempt *agentAttempt, output provider.Output, err error) bool {
	return attempt.input.Purpose == "provider_test" && err == nil && strings.TrimSpace(output.Text) == ""
}
func (a *agentAttempt) finish(output provider.Output, runErr error, started time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	record, err := a.Store.Get(ctx, "sessions", a.id)
	if err != nil {
		return err
	}
	current, err := store.Decode[model.Attempt](record)
	if err != nil {
		return err
	}
	if !sameRuntimeOwner(current, a.state) || !runtimeLeaseActive(current, time.Now().UTC()) {
		return store.ErrConflict
	}
	final, err := a.Store.Capture(ctx, a.id, "final_output", "text/plain", []byte(output.Text), store.MessagePayloadLimit, false)
	if err != nil {
		return err
	}
	activeElapsed := a.updateFinalState(output, runErr, started, final.ID)
	if output.Usage == nil {
		output.Usage = a.state.Usage
	}
	a.state.Usage = output.Usage
	metric := a.finalMetric(output, started, runErr)
	entry := a.finalUsageEntry(output, activeElapsed)
	changes := []store.Change{{Kind: "sessions", ID: a.id, Value: a.state, Expected: record.Version}, {Kind: "metrics", ID: a.id, Value: metric}}
	recoveryStatus, retryAt := "complete", ""
	failureKind := ""
	if runErr != nil {
		recoveryStatus = "failed"
		kind := provider.ClassifyFailure(runErr)
		if errors.Is(runErr, ErrRuntimeHealthLost) && a.ctx.Err() == nil {
			classified, classificationErr := a.failureKind(runErr)
			if classificationErr != nil {
				return classificationErr
			}
			kind = classified
		}
		failureKind = string(kind)
		if errors.Is(runErr, ErrHumanDecisionPending) {
			recoveryStatus = "paused"
			failureKind = "human_decision_pending"
		} else if retryableEpisodeFailure(model.RecoveryEpisode{FailureKind: failureKind, Error: runErr.Error()}) && a.state.RestartCount < 3 {
			retryAt = retryDeadline(a.state.RestartCount + 1).Format(time.RFC3339Nano)
		}
	}
	recoveryChange, recoveryErr := a.recoveryEpisodeChange(ctx, recoveryStatus, failureKind, retryAt)
	if recoveryErr != nil {
		return recoveryErr
	}
	changes = append(changes, recoveryChange)
	return a.Store.CompleteUsage(ctx, entry, changes)
}

func (a *agentAttempt) updateFinalState(output provider.Output, runErr error, started time.Time, artifactID string) int64 {
	if output.SessionID != "" {
		a.state.SessionID = output.SessionID
	}
	a.state.Finished = true
	a.state.Status = "complete"
	a.state.OutputArtifact = artifactID
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
		if errors.Is(runErr, ErrHumanDecisionPending) {
			a.state.Status = "paused"
		} else if callerCancelled(a.ctx, runErr) {
			a.state.Status = "cancelled"
		}
	}
	return activeElapsed
}

func callerCancelled(ctx context.Context, err error) bool {
	return ctx != nil && !errors.Is(err, store.ErrPersistence) && errors.Is(ctx.Err(), context.Canceled)
}

func (a *agentAttempt) finalMetric(output provider.Output, started time.Time, runErr error) map[string]any {
	tokens, cost := output.Tokens, output.Cost
	if output.Usage != nil && output.Usage.Scope == "session" {
		tokens = nil
		cost = nil
	}
	return map[string]any{"attempt_id": a.id, "project_id": a.project.ID, "task": a.task.ID, "unit_id": a.input.UnitID, "operation_id": a.input.OperationID, "role": a.input.Role, "provider": a.chosen.ID, "model": a.input.Model, "resolved_model": output.ResolvedModel, "tokens": tokens, "cost": cost, "usage": output.Usage, "elapsed_ms": time.Since(started).Milliseconds(), "success": runErr == nil, "review_attempts": a.task.ReviewPasses, "category": a.index.Category(), "risk": a.task.Risk}
}

func (a *agentAttempt) finalUsageEntry(output provider.Output, activeElapsed int64) model.UsageEntry {
	entry := model.UsageEntry{Transport: a.state.Transport, ExecutionProfile: a.state.ExecutionProfile, PlanVersion: a.task.PlanVersion, AttemptID: a.id, TaskID: a.task.ID, ProjectID: a.project.ID, OperationID: a.input.OperationID, Provider: a.chosen.ID, Model: output.ResolvedModel, Role: a.input.Role, SessionID: output.SessionID, Resumed: a.state.RequestedSessionID != "", Usage: output.Usage, ElapsedMS: activeElapsed}
	if entry.Model == "" {
		entry.Model = a.input.Model
	}
	if entry.Usage == nil && (output.Tokens != nil || output.Cost != nil) {
		entry.Usage = &model.Usage{Scope: "attempt", TotalTokens: output.Tokens, Cost: output.Cost}
	}
	return entry
}
