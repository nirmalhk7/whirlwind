package workflow

import (
	"context"
	"errors"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestPreparationAttemptHasHardDeadlineIndependentOfJobTimeout(t *testing.T) {
	start := time.Now()
	started := time.Now().UTC()
	attempt := &agentAttempt{agentRun: &agentRun{ctx: context.Background(), input: providerInputForDeadline("plan")}, state: model.Attempt{StartedAt: started.Format(time.RFC3339Nano), DeadlineAt: started.Add(attemptWallClockLimit).Format(time.RFC3339Nano)}}
	ctx, cancel, err := attempt.activeBudgetContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("preparation attempt has no persisted wall-clock deadline")
	}
	remaining := deadline.Sub(start)
	if remaining < 5*time.Minute-time.Second || remaining > 5*time.Minute+time.Second {
		t.Fatalf("preparation attempt deadline = %v from start, want 5 minutes", remaining)
	}
}

func TestPreparationAttemptUsesPersistedDeadlineAfterRestart(t *testing.T) {
	started := time.Now().Add(-4 * time.Minute).UTC()
	deadline := time.Now().Add(40 * time.Millisecond).UTC()
	attempt := &agentAttempt{agentRun: &agentRun{ctx: context.Background(), input: providerInputForDeadline("ideate")}, state: model.Attempt{StartedAt: started.Format(time.RFC3339Nano), DeadlineAt: deadline.Format(time.RFC3339Nano)}}
	ctx, cancel, err := attempt.activeBudgetContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	got, ok := ctx.Deadline()
	if !ok || !got.Equal(deadline) {
		t.Fatalf("recovered deadline = %v, want persisted %v", got, deadline)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("recovered attempt received a fresh five-minute budget")
	}
}

func TestLegacyAttemptDeadlineUsesPersistedStartTime(t *testing.T) {
	started := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	got, err := attemptDeadline(model.Attempt{StartedAt: started.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 6, 12, 5, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("legacy deadline = %s, want %s", got, want)
	}
}

func TestRuntimeAttemptDeadlineCapsConfiguredBudget(t *testing.T) {
	e, task, _ := testEngine(t)
	started := time.Now().UTC()
	state := model.Attempt{AttemptID: "attempt", StartedAt: started.Format(time.RFC3339Nano), DeadlineAt: started.Add(attemptWallClockLimit).Format(time.RFC3339Nano)}
	if err := e.Store.Put(context.Background(), "budget_reservations", state.AttemptID, model.BudgetReservation{RuntimeSeconds: 900}, 0); err != nil {
		t.Fatal(err)
	}
	attempt := &agentAttempt{agentRun: &agentRun{Engine: e, ctx: context.Background(), task: task, input: providerInputForDeadline("code")}, id: state.AttemptID, state: state}
	ctx, cancel, err := attempt.activeBudgetContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Sub(started) > attemptWallClockLimit+time.Second {
		t.Fatalf("runtime attempt deadline = %v, exceeds 5 minutes", deadline)
	}
	if attemptDeadlineExpired(state, errors.New("tool failed")) {
		t.Fatal("non-timeout execution error was treated as hard deadline expiry")
	}
}

func TestExpiredAttemptDeadlineRoutesPastCurrentChoice(t *testing.T) {
	state := model.Attempt{StartedAt: time.Now().Add(-attemptWallClockLimit - time.Second).UTC().Format(time.RFC3339Nano)}
	if !attemptDeadlineExpired(state, context.DeadlineExceeded) {
		t.Fatal("expired attempt was not recognized for direct fallback")
	}
	if attemptDeadlineExpired(state, errors.New("transport failed")) {
		t.Fatal("transport failure bypassed restart budget")
	}
}

func providerInputForDeadline(role string) provider.Input { return provider.Input{Role: role} }
