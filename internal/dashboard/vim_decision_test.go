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
	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

const (
	vimSavePlanOperation = "plan.document.save"
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
			assertInboxSelectionEditsProposalDecision(t, action)
		})
	}
}

func assertInboxSelectionEditsProposalDecision(t *testing.T, action string) {
	t.Helper()
	reason := rejectionReason(action)
	capture := decisionVim(t, action, reason)
	d := New()
	dashboardScreen(t, d)
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return json.RawMessage(`{}`), nil
	})
	direction := model.Direction{ID: "proposal", ProjectID: "p", Title: "Detailed proposal", Description: "Full\nmultiline explanation", Problem: "Problem evidence", Mechanism: "Concrete mechanism", Components: []string{"src/index.go"}}
	snapshot := control.Snapshot{"projects": {record("p", model.Project{ID: "p"})}, "directions": {record("proposal", direction)}}
	snapshot["directions"][0].Version = 7
	d.Present(snapshot)
	d.inbox.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), nil)
	assertDecisionDocument(t, capture, direction)
	request := receiveProposalDecision(t, requests, action, d)
	assertProposalDecisionRequest(t, request, action, reason)
	if d.modal != nil {
		t.Fatal("old approval modal still opened")
	}
}

func rejectionReason(action string) string {
	if action == "reject" {
		return "Already covered\nKeep existing tests.\n"
	}
	return ""
}

func assertDecisionDocument(t *testing.T, capture string, direction model.Direction) {
	t.Helper()
	body, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("selection did not open Vim: %v", err)
	}
	for _, want := range []string{direction.Title, direction.Description, direction.Problem, direction.Mechanism, "src/index.go", "Decision: cancel", ":wq"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("decision document missing %q", want)
		}
	}
}

func receiveProposalDecision(t *testing.T, requests <-chan control.Request, action string, d *Dashboard) control.Request {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(time.Second):
		t.Fatalf("saved %s decision was not submitted: %s", action, d.status.GetText(false))
		return control.Request{}
	}
}

func assertProposalDecisionRequest(t *testing.T, request control.Request, action, reason string) {
	t.Helper()
	if request.Operation != "direction."+action || request.ResourceID != "proposal" || request.ExpectedVersion != 7 || request.IdempotencyKey == "" {
		t.Fatalf("wrong decision authority: %+v", request)
	}
	if action != "reject" {
		return
	}
	var payload struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(request.Payload, &payload) != nil || payload.Reason != strings.TrimSpace(reason) {
		t.Fatalf("rejection reason lost: %s", request.Payload)
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
	capture := markdownVim(t)
	d := New()
	dashboardScreen(t, d)
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, r control.Request) (json.RawMessage, error) {
		requests <- r
		return json.RawMessage(`{}`), nil
	})
	plan := model.ExecutionPlan{ID: "task", ProjectID: "p", PlanDocument: &model.PlanDocument{Path: dashboardReviewPlanPath, Hash: dashboardReviewedHash}}
	content := "# Full plan [red] literal\n\nDetailed implementation and acceptance checks."
	planRecord := record("task", plan)
	planRecord.Version = 9
	d.Present(control.Snapshot{"projects": {record("p", model.Project{ID: "p"})}, "execution_plans": {planRecord}})
	d.showPlanReview(plan, content, dashboardReviewedHash, 9)
	d.modalInput(tcell.NewEventKey(tcell.KeyF6, 0, tcell.ModNone))
	d.modalInput(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	body, err := os.ReadFile(capture)
	if err != nil || !strings.Contains(string(body), content) {
		t.Fatalf("Vim lost exact plan: %v", err)
	}
	select {
	case r := <-requests:
		if r.Operation != vimSavePlanOperation || r.PlanID != "task" || r.ExpectedVersion != 9 || r.ContentHash != dashboardReviewedHash {
			t.Fatalf("wrong review authority: %+v", r)
		}
		var payload map[string]string
		_ = json.Unmarshal(r.Payload, &payload)
		if payload["content"] != content+"\n\nEdited implementation detail.\n" {
			t.Fatalf("revision feedback lost: %s", r.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("saved revision not submitted")
	}
}

func TestInboxExecutionPlanSelectionLoadsExactPlanForVimDecision(t *testing.T) {
	capture := markdownVim(t)
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	screen.SetSize(140, 45)
	plan := model.ExecutionPlan{ID: "task", ProjectID: "p", Stage: "plan_approval", PlanDocument: &model.PlanDocument{Path: dashboardReviewPlanPath, Hash: "exact-hash"}}
	d.Present(control.Snapshot{
		"projects":        {record("p", model.Project{ID: "p"})},
		"execution_plans": {record("task", plan)},
		"inbox":           {record("notification", model.Notification{TaskID: "task", Role: "plan", Kind: "plan", Message: "Review proposed plan"})},
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
	deadline := time.Now().Add(time.Second)
	state := readPlanReviewControlState(d)
	for !state.hasForm && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		state = readPlanReviewControlState(d)
	}
	if !state.hasForm {
		t.Fatal("Inbox plan review controls did not open")
	}
	screen.InjectKey(tcell.KeyF6, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyRune, 'v', tcell.ModNone)
	select {
	case r := <-requests:
		if r.Operation != vimSavePlanOperation || r.ContentHash != "exact-hash" {
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

func TestPlanReviewVimShortcutUsesCapturedPlanAndVersion(t *testing.T) {
	capture := markdownVim(t)
	d := New()
	dashboardScreen(t, d)
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return json.RawMessage(`{}`), nil
	})
	plan := model.ExecutionPlan{ID: "task", ProjectID: "p", PlanDocument: &model.PlanDocument{Path: dashboardReviewPlanPath, Hash: dashboardReviewedHash}}
	content := "# Full plan [red] literal\n\nDetailed implementation and acceptance checks."
	planRecord := record("task", plan)
	planRecord.Version = 9
	d.Present(control.Snapshot{"projects": {record("p", model.Project{ID: "p"})}, "execution_plans": {planRecord}})
	d.showPlanReview(plan, content, dashboardReviewedHash, 9)
	d.modalInput(tcell.NewEventKey(tcell.KeyF6, 0, tcell.ModNone))
	d.modalInput(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	body, err := os.ReadFile(capture)
	if err != nil || !strings.Contains(string(body), content) {
		t.Fatalf("Vim route did not receive the exact plan: %v", err)
	}
	select {
	case request := <-requests:
		var payload map[string]string
		_ = json.Unmarshal(request.Payload, &payload)
		if request.Operation != vimSavePlanOperation || request.PlanID != "task" || request.ExpectedVersion != 9 || request.ContentHash != dashboardReviewedHash || payload["content"] != content+"\n\nEdited implementation detail.\n" {
			t.Fatalf("Vim review lost captured plan authority or feedback: %+v payload=%v", request, payload)
		}
	case <-time.After(time.Second):
		t.Fatal("Vim revision decision was not submitted")
	}
}

func TestPlanReviewFeedbackCanContainVWithoutOpeningVim(t *testing.T) {
	capture := decisionVim(t, "cancel", "")
	d := New()
	dashboardScreen(t, d)
	plan := model.ExecutionPlan{ID: "task", ProjectID: "p", PlanDocument: &model.PlanDocument{Path: dashboardReviewPlanPath, Hash: "hash"}}
	d.Present(control.Snapshot{"projects": {record("p", model.Project{ID: "p"})}, "execution_plans": {record("task", plan)}})
	d.showPlanReview(plan, exactPlanContent, "hash", 1)
	input, ok := d.planReviewForm.GetFormItem(0).(*tview.InputField)
	if !ok {
		t.Fatalf("feedback field missing: %T", d.planReviewForm.GetFormItem(0))
	}
	d.modalInput(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	input.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone), nil)
	if input.GetText() != "v" {
		t.Fatalf("typed v was not retained in feedback: %q", input.GetText())
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatalf("typed feedback launched Vim unexpectedly: %v", err)
	}
}
