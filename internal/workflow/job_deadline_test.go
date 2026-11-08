package workflow

import (
	"context"
	"io"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// Each ACP attempt is bounded independently; discovery and fallback orchestration
// must not consume a single five-minute deadline for the whole ideation run.
func TestIdeationDoesNotWrapWholeRunInFiveMinuteDeadline(t *testing.T) {
	f := prepareNoveltyTest(t, []string{`{"unique":false}`, `{"unique":true}`})
	regenerated := ideaBatch(t)
	for index := range regenerated {
		regenerated[index].Problem = "Distinct " + regenerated[index].Category + " problem found during reconsideration"
		regenerated[index].Mechanism = "Apply a different " + regenerated[index].Category + " solution"
		regenerated[index].Components = []string{"internal/reconsidered/" + regenerated[index].Category}
	}
	f.fixture.batches = [][]model.Direction{f.fixture.batch, regenerated}
	source := &deadlineSource{t: t, started: time.Now()}
	f.engine.GitHub = source
	var previousDeadline time.Time
	f.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
			previousDeadline = assertFreshAttemptDeadline(t, ctx, previousDeadline)
			if in.Purpose == "generation" && ctx.Err() != nil {
				t.Fatalf("fallback generation context ended early: %v", ctx.Err())
			}
			return f.fixture.Run(ctx, in, hooks)
		})
	}
	if err := f.engine.Ideate(context.Background(), f.project, "2026-10-29"); err != nil {
		t.Fatal(err)
	}
	if f.fixture.calls != 4 {
		t.Fatalf("generation/novelty retries not exercised: %d", f.fixture.calls)
	}
}

type deadlineSource struct {
	fakeGitHub
	t                 *testing.T
	started, deadline time.Time
}

func (s *deadlineSource) Sources(ctx context.Context, _ string) (any, error) {
	deadline, ok := ctx.Deadline()
	if ok {
		s.t.Fatalf("ideation discovery inherited a whole-run deadline: %v remaining", deadline.Sub(s.started))
	}
	s.deadline = deadline
	return map[string]any{}, nil
}

// Each restart and model fallback gets its own bounded execution attempt.
func TestPlannerAttemptsReceiveFreshFiveMinuteDeadlinesAcrossFallback(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	chain := model.AgentConfig{Role: "plan", Chain: []model.AgentChoice{{Provider: "fake", Model: "first"}, {Provider: "fake", Model: "next"}}}
	putFixtureAgent(t, e.Store, "plan", chain)
	e.retryWait = func(context.Context, time.Duration) error { return nil }
	var deadline time.Time
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
			deadline = assertFreshAttemptDeadline(t, ctx, deadline)
			wantModel := "first"
			if calls == 4 {
				wantModel = "next"
			}
			if in.Model != wantModel {
				t.Fatalf("attempt %d selected model %q, want %q", calls+1, in.Model, wantModel)
			}
			calls++
			if calls <= 4 {
				return provider.Output{}, io.EOF
			}
			return (fakeAgent{9}).Run(ctx, in, hooks)
		})
	}
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if calls != 5 {
		t.Fatalf("provider fallback not exercised: %d", calls)
	}
	record, _ := e.Store.Get(ctx, "tasks", task.ID)
	saved, _ := store.Decode[model.Task](record)
	if saved.Stage != "plan_approval" || saved.Plan == nil || saved.PlanDocument == nil || saved.PlanDocument.ApprovedHash != "" {
		t.Fatal("successful planning failed to persist for human approval")
	}
}

func assertFiveMinuteDeadline(t *testing.T, ctx context.Context, started time.Time) time.Time {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("planner has no job deadline")
	}
	remaining := deadline.Sub(started)
	if remaining < 5*time.Minute-time.Second {
		t.Fatalf("planner deadline unexpectedly short: %v", remaining)
	}
	if remaining > 5*time.Minute+time.Second {
		t.Fatalf("planner deadline exceeds five minutes: %v", remaining)
	}
	return deadline
}
func assertFreshAttemptDeadline(t *testing.T, ctx context.Context, previous time.Time) time.Time {
	t.Helper()
	got := assertFiveMinuteDeadline(t, ctx, time.Now())
	if !previous.IsZero() && !got.After(previous) {
		t.Fatalf("new attempt reused an earlier deadline: got %v previous %v", got, previous)
	}
	return got
}
