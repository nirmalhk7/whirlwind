package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

const (
	dashboardReviewPlanPath = "PLAN.md"
	exactPlanContent        = "exact plan"
	dashboardPlanReviewOp   = "plan.review"
	dashboardReviewedHash   = "reviewed-hash"
)

func TestPlanReviewShowsExactContentAndDecisionControlsAtSmallSizes(t *testing.T) {
	for _, size := range [][2]int{{100, 36}, {70, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			decisionVim(t, "cancel", "")
			d := New()
			screen := dashboardScreen(t, d)
			screen.SetSize(size[0], size[1])
			plan := model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Path: dashboardReviewPlanPath, Hash: dashboardHashOne}}
			d.Present(control.Snapshot{
				"projects":        {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
				"execution_plans": {record(dashboardPlanOne, plan)},
			})
			content := "# Exact heading [red] literal\n\nSecond line"
			d.showPlanReview(plan, content, dashboardHashOne, 1)
			d.ForceDraw()
			text := screenText(screen)
			for _, want := range []string{dashboardReviewPlanPath, dashboardHashOne, "Version: 1", "# Exact heading [red] literal", "Second line", "Approve", "Request changes", "Reject", "Cancel"} {
				if !strings.Contains(text, want) {
					t.Errorf("%dx%d plan review omitted %q:\n%s", size[0], size[1], want, text)
				}
			}
		})
	}
}

func TestPlanReviewDecisionControlsRetainHashVersionAndFeedback(t *testing.T) {
	for index, action := range []struct {
		label, value string
	}{
		{"Approve", "approve"},
		{"Request changes", "revise"},
		{"Reject", "reject"},
	} {
		t.Run(action.value, func(t *testing.T) {
			assertPlanReviewDecision(t, action.value, action.label, index+1)
		})
	}
}

func assertPlanReviewDecision(t *testing.T, action, label string, tabs int) {
	t.Helper()
	decisionVim(t, "cancel", "")
	d := New()
	dashboardScreen(t, d)
	plan := model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Path: dashboardReviewPlanPath, Hash: dashboardHashOne}}
	d.Present(control.Snapshot{
		"projects":        {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
		"execution_plans": {record(dashboardPlanOne, plan)},
	})
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return json.RawMessage(`{}`), nil
	})
	d.showPlanReview(plan, exactPlanContent, dashboardHashOne, 1)
	focused, ok := d.modal.(*tview.Form)
	if !ok {
		t.Fatalf("plan review did not expose its decision controls: %T", d.modal)
	}
	feedback, ok := focused.GetFormItem(0).(*tview.InputField)
	if !ok {
		t.Fatal("plan review feedback input is missing")
	}
	enterPlanReviewFeedback(feedback, "please adjust the rollout")
	focusPlanReviewAction(d, focused, tabs)
	focused.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	request := receivePlanReviewRequest(t, requests)
	var payload map[string]string
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if request.Operation != dashboardPlanReviewOp || request.ProjectID != dashboardProjectOne || request.ExpectedVersion != 1 || request.ContentHash != dashboardHashOne || payload["hash"] != dashboardHashOne || payload["action"] != action || payload["feedback"] != "please adjust the rollout" {
		t.Fatalf("%s lost exact reviewed plan identity or feedback: request=%+v payload=%v", label, request, payload)
	}
}

func enterPlanReviewFeedback(feedback *tview.InputField, value string) {
	for _, char := range value {
		feedback.InputHandler()(tcell.NewEventKey(tcell.KeyRune, char, tcell.ModNone), nil)
	}
}

func focusPlanReviewAction(d *Dashboard, form *tview.Form, tabs int) {
	for range tabs {
		form.InputHandler()(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	}
}

func receivePlanReviewRequest(t *testing.T, requests <-chan control.Request) control.Request {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("decision control did not submit a review")
		return control.Request{}
	}
}

func TestPlanReviewDoesNotSubmitAfterPlanChangesOrCancel(t *testing.T) {
	decisionVim(t, "cancel", "")
	plan := model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Path: dashboardReviewPlanPath, Hash: dashboardHashOne}}
	d := New()
	dashboardScreen(t, d)
	d.Present(control.Snapshot{
		"projects":        {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
		"execution_plans": {record(dashboardPlanOne, plan)},
	})
	called := false
	d.SetCommandHandler(func(_ context.Context, _ control.Request) (json.RawMessage, error) {
		called = true
		return json.RawMessage(`{}`), nil
	})
	d.showPlanReview(plan, exactPlanContent, dashboardHashOne, 1)
	cancel, ok := d.modal.(*tview.Form)
	if !ok {
		t.Fatalf("plan review did not expose Cancel: %T", d.modal)
	}
	for range 4 {
		cancel.InputHandler()(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	}
	cancel.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	if called || d.modal != nil {
		t.Fatal("cancel submitted a plan review or left review screen open")
	}

	d.showPlanReview(plan, exactPlanContent, dashboardHashOne, 1)
	d.snapshot["execution_plans"][0] = record(dashboardPlanOne, model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Hash: "changed"}})
	focused, ok := d.modal.(*tview.Form)
	if !ok {
		t.Fatalf("plan review did not expose its decision controls: %T", d.modal)
	}
	focused.InputHandler()(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	focused.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	if called || !strings.Contains(d.status.GetText(false), "plan changed") {
		t.Fatal("stale plan review submitted or failed to explain the stale content")
	}
	if d.modal != nil {
		t.Fatal("stale plan review remained open after refusing submission")
	}
}

func TestPlanReviewCanScrollExactPlanThenReturnToDecisionControls(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	screen.SetSize(70, 20)
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return json.RawMessage(`{}`), nil
	})
	plan := model.ExecutionPlan{ID: dashboardPlanOne, ProjectID: dashboardProjectOne, PlanDocument: &model.PlanDocument{Path: dashboardReviewPlanPath, Hash: dashboardHashOne}}
	d.Present(control.Snapshot{
		"projects":        {record(dashboardProjectOne, model.Project{ID: dashboardProjectOne, Name: "Repo"})},
		"execution_plans": {record(dashboardPlanOne, plan)},
	})
	var content strings.Builder
	for line := 1; line <= 25; line++ {
		fmt.Fprintf(&content, "Plan section %02d: preserve existing implementation and verify behavior.\n", line)
	}
	content.WriteString("Final acceptance check: retain exact hash and plan version.")
	d.showPlanReview(plan, content.String(), dashboardHashOne, 1)
	finished := make(chan error, 1)
	go func() { finished <- d.Run() }()
	t.Cleanup(func() {
		d.Stop()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("dashboard did not stop after plan review test")
		}
	})
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(readDashboardScreenText(d, screen), "Plan section 01") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	screen.InjectKey(tcell.KeyF6, 0, tcell.ModNone)
	for range 4 {
		screen.InjectKey(tcell.KeyPgDn, 0, tcell.ModNone)
	}
	deadline = time.Now().Add(time.Second)
	for !strings.Contains(readDashboardScreenText(d, screen), "Final acceptance check") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if screen := readDashboardScreenText(d, screen); !strings.Contains(screen, "Final acceptance check") {
		t.Fatalf("PageDown could not reach the end of the exact plan:\n%s", screen)
	}
	screen.InjectKey(tcell.KeyF6, 0, tcell.ModNone)
	deadline = time.Now().Add(time.Second)
	state := readPlanReviewControlState(d)
	for !state.focused && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		state = readPlanReviewControlState(d)
	}
	if !state.hasForm {
		t.Fatal("plan review modal does not expose decision controls")
	}
	if !state.focused {
		t.Fatal("Tab from the plan text did not return keyboard focus to decision controls")
	}
	screen.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	select {
	case request := <-requests:
		var payload map[string]string
		if err := json.Unmarshal(request.Payload, &payload); err != nil || payload["action"] != "approve" {
			t.Fatalf("Tab navigation did not reach visible Approve control: payload=%v err=%v", payload, err)
		}
	case <-time.After(time.Second):
		t.Fatal("keyboard could not reach Approve after scrolling the plan")
	}
}

func readPlanReviewControlState(d *Dashboard) (state struct{ hasForm, focused bool }) {
	d.QueueUpdateDraw(func() {
		state.hasForm = d.planReviewForm != nil
		form, ok := d.modal.(*tview.Form)
		state.focused = ok && form.HasFocus()
	})
	return state
}

func readDashboardScreenText(d *Dashboard, screen tcell.SimulationScreen) (text string) {
	d.QueueUpdateDraw(func() { text = screenText(screen) })
	return text
}
