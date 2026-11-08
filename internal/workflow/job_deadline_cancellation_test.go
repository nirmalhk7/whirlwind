package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// A project cannot extend the five-minute ceiling.
func TestPlannerJobCapsLongerProjectTimeout(t *testing.T) {
	e, task := plannerWithTimeout(t, 3600)
	configurePlannerQueue(t, e)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
			assertFiveMinuteDeadline(t, ctx, time.Now())
			return deadlinePlannerAgent{}.Run(ctx, in, hooks)
		})
	}
	if err := e.Step(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
}

type deadlinePlannerAgent struct{}

func (deadlinePlannerAgent) Run(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
	output, err := (fakeAgent{9}).Run(ctx, in, hooks)
	if err != nil || in.Role != "plan" {
		return output, err
	}
	var plan model.Plan
	if err := json.Unmarshal([]byte(output.Text), &plan); err != nil {
		return provider.Output{}, err
	}
	plan.Requirements = []model.Requirement{{ID: "REQ-RESULT", Outcome: "The result file exists.", EvidenceMethod: "Run the planner deadline test."}}
	plan.Units[0].AllowedPaths = []string{resultFile}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return provider.Output{}, err
	}
	output.Text = string(encoded)
	return output, nil
}

// Cancellation from an enclosing operation still finalizes the provider and
// never saves an unfinished plan.
func TestPlannerJobCancellationFinalizesAttemptWithoutSavingPlan(t *testing.T) {
	e, task, _ := testEngine(t)
	configurePlannerQueue(t, e)
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, _ provider.Input, _ provider.Hooks) (provider.Output, error) {
			calls++
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := e.Step(ctx, task.ID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("planner did not time out: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expired planner retried: %d calls", calls)
	}
	assertTimedOutPlanNotSaved(t, e, task.ID)
}

func configurePlannerQueue(t *testing.T, e *Engine) {
	t.Helper()
	putFixtureAgent(t, e.Store, "plan", model.AgentConfig{Role: "plan", Chain: []model.AgentChoice{{Provider: "fake", Model: "fixture"}}})
}

func plannerWithTimeout(t *testing.T, seconds int) (*Engine, model.Task) {
	t.Helper()
	e, task, _ := testEngine(t)
	ctx := context.Background()
	record, _ := e.Store.Get(ctx, "projects", task.ProjectID)
	p, _ := store.Decode[model.Project](record)
	p.Policy.RunTimeoutSeconds = seconds
	if err := e.Store.Put(ctx, "projects", p.ID, p, record.Version); err != nil {
		t.Fatal(err)
	}
	return e, task
}

func assertTimedOutPlanNotSaved(t *testing.T, e *Engine, id string) {
	t.Helper()
	ctx := context.Background()
	record, err := e.Store.Get(ctx, "tasks", id)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := store.Decode[model.Task](record)
	if task.Plan != nil || task.Stage != "planning" {
		t.Fatal("timed out planner advanced task")
	}
	sessions, err := e.Store.List(ctx, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("unexpected planner attempts: %d", len(sessions))
	}
	attempt, _ := store.Decode[model.Attempt](sessions[0])
	if !attempt.Finished || attempt.Status != "failed" {
		t.Fatalf("timed out attempt not finalized: %+v", attempt)
	}
}
