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

func TestReadyPlanUsesLocalApprovalAndRetainsFullSpecification(t *testing.T) {
	e, original, _ := testEngine(t)
	e.PlansRoot = t.TempDir()
	var plan model.Plan
	if err := json.Unmarshal([]byte(`{"objective":"Improve search","specification":"Ignore stale responses after area selection changes. Add delayed-response regression coverage.","acceptance_criteria":["Stale response never replaces current area"],"requirements":[{"id":"REQ-SEARCH","outcome":"A stale response never replaces the current area.","evidence_method":"Run TestSearchIgnoresStaleAreaResponse"}],"units":[{"id":"search","objective":"Guard search responses and add delayed-response tests","allowed_paths":["src/search.ts","src/search.test.ts"],"dependencies":[]}]}`), &plan); err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "ready-plan", ProjectID: original.ProjectID, Mode: model.PersonalMode, PlanVersion: 1, Stage: "plan_ready", Plan: &plan, Issue: model.Issue{Title: "Improve search"}}
	task.Selection = original.Selection
	if err := e.Store.Put(context.Background(), "tasks", task.ID, task, 0); err != nil {
		t.Fatal(err)
	}
	g := &readyPlanGitHub{}
	e.GitHub = g
	if err := e.Step(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Store.Get(context.Background(), "tasks", task.ID)
	updated, _ := store.Decode[model.Task](r)
	if updated.Stage != "plan_approval" || updated.PlanDocument == nil || updated.PlanDocument.ApprovedHash != "" || g.body != "" || updated.Issue.Number != 0 {
		t.Fatalf("ready plan bypassed local approval or posted remotely: %+v issue=%q", updated, g.body)
	}
	view, err := e.ReadLocalPlan(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Specification", "Ignore stale responses", "## Acceptance criteria", "Stale response never replaces current area", "src/search.test.ts", "Guard search responses", "REQ-SEARCH"} {
		if !strings.Contains(view.Content, want) {
			t.Fatalf("local plan document missing %q: %s", want, view.Content)
		}
	}
}

func TestPersonalPlanMustBeLocallyApprovedBeforeCoding(t *testing.T) {
	e, original, _ := testEngine(t)
	e.PlansRoot = t.TempDir()
	var plan model.Plan
	if err := json.Unmarshal([]byte(`{"objective":"Improve search","what":"Ignore stale search responses","why":"Old responses overwrite current data","benefit":"Selected area stays current","where":["internal/search.go"],"specification":"Ignore stale responses after area selection changes.","acceptance_criteria":["Stale response never replaces current area"],"units":[{"id":"search","objective":"Add delayed-response regression test and guard stale results","allowed_paths":["internal/search.go"]}],"requirements":[{"id":"REQ-SEARCH-1","outcome":"A stale search response never replaces the current selection.","evidence_method":"Run TestSearchIgnoresStaleAreaResponse"}]}`), &plan); err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "personal-ready-plan", ProjectID: original.ProjectID, Mode: model.PersonalMode, PlanVersion: 1, Stage: "plan_ready", Plan: &plan, Issue: model.Issue{Title: "Improve search"}, Selection: original.Selection}
	if err := e.Store.Put(context.Background(), "tasks", task.ID, task, 0); err != nil {
		t.Fatal(err)
	}
	g := &readyPlanGitHub{}
	e.GitHub = g
	if err := e.Step(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	updated, _ := localStoredTask(t, e, task.ID)
	if updated.Stage != "plan_approval" || updated.PlanDocument == nil || updated.PlanDocument.ApprovedHash != "" || g.body != "" {
		t.Fatalf("Personal plan bypassed local approval or published remotely: %+v issue=%q", updated, g.body)
	}
}
