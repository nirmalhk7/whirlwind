package workflow

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestEmptyIdeationPersistsIncompleteWithoutBudgetSetup(t *testing.T) {
	h := preparationHarness(t, false)
	ctx := context.Background()
	putFixtureAgent(t, h.engine.Store, "ideate", model.AgentConfig{Role: "ideate", Chain: []model.AgentChoice{{Provider: "fake", Model: "fixture"}}})
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, _ provider.Input, _ provider.Hooks) (provider.Output, error) {
			assertFiveMinuteDeadline(t, ctx, time.Now())
			return provider.Output{Text: "[]"}, nil
		})
	}
	if err := h.engine.Ideate(ctx, h.project, "2026-10-29"); err == nil {
		t.Fatal("empty proposal set was marked successful")
	}
	record, err := h.engine.Store.Get(ctx, "schedules", h.project.ID+":2026-10-29")
	if err != nil {
		t.Fatal(err)
	}
	var schedule struct{ Stage string }
	if err := json.Unmarshal(record.Data, &schedule); err != nil {
		t.Fatal(err)
	}
	if schedule.Stage != "incomplete" {
		t.Fatalf("empty ideation not marked incomplete: %s", record.Data)
	}
}

func TestPlannerCompletesWithoutBudgetSetup(t *testing.T) {
	h := preparationHarness(t, false)
	if err := h.engine.Step(context.Background(), h.task.ID); err != nil {
		t.Fatal(err)
	}
	assertPlannerCompleted(t, h)
}

// Failed attempts and provider fallback cannot exhaust an obsolete attempt cap.
func TestPreparationFallbackIgnoresAttemptBudget(t *testing.T) {
	for _, role := range []string{"plan", "ideate"} {
		t.Run(role, func(t *testing.T) { assertPreparationFallback(t, role) })
	}
}

func assertPreparationFallback(t *testing.T, role string) {
	t.Helper()
	h := preparationHarness(t, true)
	ctx := context.Background()
	config := model.AgentConfig{Role: role, Chain: []model.AgentChoice{{Provider: "fake", Model: "first"}, {Provider: "fake", Model: "next"}}}
	putFixtureAgent(t, h.engine.Store, role, config)
	calls := 0
	var deadline time.Time
	h.engine.Adapter = func(model.Provider) provider.Adapter {
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
			if role == "plan" {
				return (fakeAgent{9}).Run(ctx, in, hooks)
			}
			return provider.Output{Text: "recovered"}, nil
		})
	}
	out, err := h.engine.agent(ctx, h.project, h.task, provider.Input{Role: role, CWD: h.cwd, Prompt: "prepare", ReadOnly: true})
	if err != nil || out.Text == "" {
		t.Fatalf("fallback blocked by old budget: %v", err)
	}
	if role == "plan" {
		var plan model.Plan
		if err := json.Unmarshal([]byte(out.Text), &plan); err != nil || plan.Objective != "Add file" {
			t.Fatalf("planner fallback output lost: %q %v", out.Text, err)
		}
	} else if out.Text != "recovered" {
		t.Fatalf("ideation fallback output lost: %q", out.Text)
	}
	if calls != 5 {
		t.Fatalf("unexpected attempts: %d", calls)
	}
}

func assertPlannerCompleted(t *testing.T, h retryHarness) {
	t.Helper()
	record, err := h.engine.Store.Get(context.Background(), "tasks", h.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var task model.Task
	if err := json.Unmarshal(record.Data, &task); err != nil {
		t.Fatal(err)
	}
	if task.Stage != "plan_approval" || task.Plan == nil || task.PlanDocument == nil || task.PlanDocument.ApprovedHash != "" {
		t.Fatal("planner did not save its plan for explicit human approval")
	}
	relative, err := filepath.Rel(h.engine.Root, task.PlanDocument.Path)
	if err != nil || !filepath.IsLocal(relative) {
		t.Fatalf("planner fixture wrote outside its owned root: %q %v", task.PlanDocument.Path, err)
	}
	view, err := h.engine.ReadLocalPlan(context.Background(), task.ID)
	if err != nil || view.Hash == "" || view.Plan.Objective != "Add file" || view.ApprovedHash != "" {
		t.Fatalf("saved plan is not reviewable before coding: %+v %v", view, err)
	}
}
