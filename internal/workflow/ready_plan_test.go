package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type readyPlanGitHub struct {
	fakeGitHub
	body string
}

func (g *readyPlanGitHub) CreateIssue(_ context.Context, _, title, body, _ string) (model.Issue, error) {
	g.body = body
	return model.Issue{Number: 9, Title: title, Body: body, State: "OPEN", Labels: []string{"whirlwind:ready"}}, nil
}

func TestPublishedReadyIssueRetainsFullPlanSpecification(t *testing.T) {
	e, original, _ := testEngine(t)
	var plan model.Plan
	if err := json.Unmarshal([]byte(`{"objective":"Improve search","specification":"Ignore stale responses after area selection changes. Add delayed-response regression coverage.","acceptance_criteria":["Stale response never replaces current area"],"units":[{"id":"search","objective":"Guard search responses and add delayed-response tests","allowed_paths":["src/search.ts","src/search.test.ts"],"dependencies":[]}]}`), &plan); err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "ready-plan", ProjectID: original.ProjectID, PlanVersion: 1, Stage: "plan_ready", Plan: &plan, Issue: model.Issue{Title: "Improve search"}}
	task.Selection = original.Selection
	if err := e.Store.Put(context.Background(), "tasks", task.ID, task, 0); err != nil {
		t.Fatal(err)
	}
	g := &readyPlanGitHub{}
	e.GitHub = g
	if err := e.Step(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Specification", "Ignore stale responses", "## Acceptance criteria", "Stale response never replaces current area", "src/search.test.ts", "Guard search responses"} {
		if !strings.Contains(g.body, want) {
			t.Fatalf("published issue missing %q: %s", want, g.body)
		}
	}
	r, _ := e.Store.Get(context.Background(), "tasks", task.ID)
	updated, _ := store.Decode[model.Task](r)
	if updated.Stage != "coding" || !updated.Issue.Eligible() {
		t.Fatalf("published issue not ready for coding: %+v", updated)
	}
}
