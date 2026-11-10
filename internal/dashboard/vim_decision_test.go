package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func decisionVim(t *testing.T, decision, reason string) string {
	t.Helper()
	directory := t.TempDir()
	capture := filepath.Join(directory, "reviewed")
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DECISION_CAPTURE", capture)
	t.Setenv("REVIEW_DECISION", decision)
	t.Setenv("REVIEW_REASON", reason)
	script := `#!/usr/bin/env python3
import os,sys
p=sys.argv[-1]
s=open(p).read()
open(os.environ['DECISION_CAPTURE'],'w').write(s)
if '-R' in sys.argv: sys.exit(4)
s=s.replace('Decision: cancel','Decision: '+os.environ['REVIEW_DECISION'],1)
s=s.replace('Reason:\n','Reason:\n'+os.environ['REVIEW_REASON'],1)
if not s.endswith('\n'): s+='\n'
open(p,'w').write(s)
`
	if err := os.WriteFile(filepath.Join(directory, "vim"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return capture
}

func TestInboxSelectionEditsProposalDecisionInVim(t *testing.T) {
	for _, action := range []string{"approve", "reject"} {
		t.Run(action, func(t *testing.T) {
			reason := ""
			if action == "reject" {
				reason = "Already covered\nKeep existing tests.\n"
			}
			capture := decisionVim(t, action, reason)
			d := New()
			dashboardScreen(t, d)
			requests := make(chan control.Request, 1)
			d.SetCommandHandler(func(_ context.Context, r control.Request) (json.RawMessage, error) {
				requests <- r
				return json.RawMessage(`{}`), nil
			})
			direction := model.Direction{ID: "proposal", ProjectID: "p", Title: "Detailed proposal", Description: "Full\nmultiline explanation", Problem: "Problem evidence", Mechanism: "Concrete mechanism", Components: []string{"src/index.go"}}
			snapshot := control.Snapshot{"projects": {record("p", model.Project{ID: "p"})}, "directions": {record("proposal", direction)}}
			snapshot["directions"][0].Version = 7
			d.Present(snapshot)
			d.inbox.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), nil)
			body, err := os.ReadFile(capture)
			if err != nil {
				t.Fatalf("selection did not open Vim: %v", err)
			}
			for _, want := range []string{direction.Title, direction.Description, direction.Problem, direction.Mechanism, "src/index.go", "Decision: cancel", ":wq"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("decision document missing %q", want)
				}
			}
			select {
			case request := <-requests:
				if request.Operation != "direction."+action || request.ResourceID != "proposal" || request.ExpectedVersion != 7 || request.IdempotencyKey == "" {
					t.Fatalf("wrong decision authority: %+v", request)
				}
				if action == "reject" {
					var payload struct {
						Reason string `json:"reason"`
					}
					if json.Unmarshal(request.Payload, &payload) != nil || payload.Reason != strings.TrimSpace(reason) {
						t.Fatalf("rejection reason lost: %s", request.Payload)
					}
				}
			case <-time.After(time.Second):
				t.Fatalf("saved %s decision was not submitted: %s", action, d.status.GetText(false))
			}
			if d.modal != nil {
				t.Fatal("old approval modal still opened")
			}
		})
	}
}

func TestVimProposalDecisionUnchangedOrInvalidDoesNotSubmit(t *testing.T) {
	for _, action := range []string{"cancel", "invalid"} {
		t.Run(action, func(t *testing.T) {
			decisionVim(t, action, "")
			d := New()
			dashboardScreen(t, d)
			requests := make(chan control.Request, 1)
			d.SetCommandHandler(func(_ context.Context, r control.Request) (json.RawMessage, error) { requests <- r; return nil, nil })
			d.Present(control.Snapshot{"directions": {record("idea", model.Direction{ID: "idea"})}})
			d.inbox.GetSelectedFunc()(0, "", "", 0)
			select {
			case r := <-requests:
				t.Fatalf("unexpected mutation: %+v", r)
			default:
			}
			if action == "invalid" && !strings.Contains(strings.ToLower(d.status.GetText(false)), "invalid") {
				t.Fatalf("missing actionable validation error: %s", d.status.GetText(false))
			}
		})
	}
}

func TestVimExecutionPlanRevisionRetainsExactContentAndHash(t *testing.T) {
	capture := decisionVim(t, "revise", "Cover timeout recovery.\nPreserve offline behavior.\n")
	d := New()
	dashboardScreen(t, d)
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, r control.Request) (json.RawMessage, error) {
		requests <- r
		return json.RawMessage(`{}`), nil
	})
	plan := model.Task{ID: "task", ProjectID: "p", PlanDocument: &model.PlanDocument{Path: "PLAN.md", Hash: "reviewed-hash"}}
	content := "# Full plan [red] literal\n\nDetailed implementation and acceptance checks."
	d.showPlanReview(plan, content, "reviewed-hash", 9)
	body, err := os.ReadFile(capture)
	if err != nil || !strings.Contains(string(body), content) {
		t.Fatalf("Vim lost exact plan: %v", err)
	}
	select {
	case r := <-requests:
		if r.Operation != "plan.review" || r.PlanID != "task" || r.ExpectedVersion != 9 || r.ContentHash != "reviewed-hash" {
			t.Fatalf("wrong review authority: %+v", r)
		}
		var payload map[string]string
		_ = json.Unmarshal(r.Payload, &payload)
		if payload["action"] != "revise" || payload["hash"] != "reviewed-hash" || payload["feedback"] != "Cover timeout recovery.\nPreserve offline behavior." {
			t.Fatalf("revision feedback lost: %s", r.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("saved revision not submitted")
	}
}

func TestInboxExecutionPlanSelectionLoadsExactPlanForVimDecision(t *testing.T) {
	capture := decisionVim(t, "approve", "")
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	screen.SetSize(140, 45)
	plan := model.Task{ID: "task", ProjectID: "p", Stage: "plan_approval", PlanDocument: &model.PlanDocument{Path: "PLAN.md", Hash: "exact-hash"}}
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p"})},
		"tasks":    {record("task", plan)},
		"inbox":    {record("notification", model.Notification{TaskID: "task", Role: "plan", Kind: "plan", Message: "Review proposed plan"})},
	})
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, r control.Request) (json.RawMessage, error) {
		if r.Operation == "inspect.plan" {
			return json.RawMessage(`{"hash":"exact-hash","content":"Full execution plan content"}`), nil
		}
		requests <- r
		return json.RawMessage(`{}`), nil
	})
	done := make(chan error, 1)
	go func() { done <- d.Run() }()
	defer func() { d.Stop(); <-done }()
	d.QueueUpdateDraw(func() { d.inbox.GetSelectedFunc()(0, "", "", 0) })
	select {
	case r := <-requests:
		if r.Operation != "plan.review" || r.ContentHash != "exact-hash" {
			t.Fatalf("wrong Inbox plan decision: %+v", r)
		}
		body, err := os.ReadFile(capture)
		if err != nil || !strings.Contains(string(body), "Full execution plan content") {
			t.Fatalf("Vim missing fetched plan: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Inbox execution plan did not reach Vim review")
	}
}
