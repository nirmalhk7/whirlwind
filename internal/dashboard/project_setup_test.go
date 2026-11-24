package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func TestProjectSetupFailureShowsDurableProgressAndOffersExplicitRetry(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repository", Status: "error"})},
		"operations": {record("setup-1", map[string]any{
			"kind": "project_setup", "subject": "p", "stage": "failed", "error": "Qlty could not install Gitleaks; check approved network access and retry",
			"started_at": time.Now().UTC().Format(time.RFC3339Nano),
			"input": map[string]any{"progress": map[string]any{
				"current": "gitleaks", "completed": []map[string]string{{"name": "qlty catalog", "status": "pass"}},
			}},
		})},
	})
	if len(d.runRows) != 1 || d.runRows[0].id != "setup-1" {
		t.Fatalf("project setup operation hidden from run history: %+v", d.runRows)
	}
	d.selectRun(0, "", "", 0)
	for _, want := range []string{"Project setup", "failed", "qlty catalog", "pass", "gitleaks", "retry"} {
		if !strings.Contains(strings.ToLower(d.detail.GetText(false)), strings.ToLower(want)) {
			t.Fatalf("setup progress detail missing %q: %s", want, d.detail.GetText(false))
		}
	}
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return nil, nil
	})
	d.focus = 2
	d.runs.SetCurrentItem(0)
	d.input(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone))
	select {
	case request := <-requests:
		if request.Operation != "project.setup.retry" || request.ProjectID != "p" || request.ExpectedVersion != 1 || request.IdempotencyKey == "" {
			t.Fatalf("setup retry was not scoped and versioned: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("failed setup did not submit explicit retry command")
	}
}

func TestProjectSetupHistoryNamesDurablePreparationPhases(t *testing.T) {
	for _, test := range []struct {
		name  string
		input map[string]any
		want  string
	}{
		{"prepare", map[string]any{"onboarding": true}, "Preparing quality config"},
		{"catalog", map[string]any{"onboarding": true, "phase": "catalog"}, "Recording scanner configuration"},
		{"qualify", map[string]any{"onboarding": true, "phase": "qualify"}, "Checking project"},
		{"manual", map[string]any{}, "Checking project"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := projectSetupPhaseLabel(test.input); got != test.want {
				t.Fatalf("setup phase label = %q, want %q", got, test.want)
			}
			run := ideationRunView{Operation: model.Operation{Kind: "project_setup", Subject: "p", Stage: "queued", Input: test.input}}
			if details := projectSetupRunDetails("setup", run); !strings.Contains(details, "Phase: "+test.want) {
				t.Fatalf("durable setup details omitted phase %q: %s", test.want, details)
			}
		})
	}
}

func TestCompletedProjectSetupOffersExplicitRecheck(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p", Name: "Repository", Status: "approved"})},
		"operations": {record("setup-3", map[string]any{"kind": "project_setup", "subject": "p", "stage": "complete", "input": map[string]any{"onboarding": true, "phase": "qualify"}})},
	})
	d.selectRun(0, "", "", 0)
	if !strings.Contains(d.detail.GetText(false), "Press r in Runs to explicitly recheck") {
		t.Fatalf("completed setup lacks visible recheck action: %s", d.detail.GetText(false))
	}
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return nil, nil
	})
	d.focus = 2
	d.runs.SetCurrentItem(0)
	d.input(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone))
	select {
	case request := <-requests:
		if request.Operation != "project.setup.retry" || request.ProjectID != "p" || request.ExpectedVersion != 1 {
			t.Fatalf("completed setup recheck request is incorrect: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("completed setup did not submit explicit recheck")
	}
}
