package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func reconsiderationDashboard(t *testing.T) (*Dashboard, control.Snapshot, tcell.SimulationScreen) {
	t.Helper()
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(120, 35)
	snapshot := control.Snapshot{
		"projects": {
			record("project-one", model.Project{ID: "project-one", Name: "Current project"}),
			record("project-two", model.Project{ID: "project-two", Name: "Other project"}),
		},
		"directions": {
			{ID: "dismissed-one", Version: 7, Data: encodedDirection(model.Direction{ID: "dismissed-one", ProjectID: "project-one", Title: "Dismissed idea", Status: "dismissed"})},
			{ID: "rejected-one", Version: 3, Data: encodedDirection(model.Direction{ID: "rejected-one", ProjectID: "project-one", Title: "Rejected idea", Status: "rejected"})},
			record("foreign-dismissed", model.Direction{ID: "foreign-dismissed", ProjectID: "project-two", Title: "Foreign history", Status: "dismissed"}),
			record("deferred-one", model.Direction{ID: "deferred-one", ProjectID: "project-one", Title: "Deferred idea", Status: "deferred"}),
			record("selected-one", model.Direction{ID: "selected-one", ProjectID: "project-one", Title: "Already selected", Status: "dismissed", Selected: true}),
			record("proposed-one", model.Direction{ID: "proposed-one", ProjectID: "project-one", Title: "Current idea", Status: "proposed"}),
		},
	}
	d.Present(snapshot)
	d.focus = 1
	d.SetFocus(d.directions)
	return d, snapshot, screen
}

func encodedDirection(direction model.Direction) json.RawMessage {
	data, _ := json.Marshal(direction)
	return data
}

func openDirectionHistory(t *testing.T, d *Dashboard) *tview.List {
	t.Helper()
	d.input(tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone))
	list, ok := d.modal.(*tview.List)
	if !ok {
		t.Fatalf("history key did not open direction history: %T", d.modal)
	}
	return list
}

func selectHistoryRow(t *testing.T, list *tview.List) {
	t.Helper()
	list.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
}

func reconsiderationForm(t *testing.T, d *Dashboard, list *tview.List) *tview.Form {
	t.Helper()
	selectHistoryRow(t, list)
	form, ok := d.modal.(*tview.Form)
	if !ok {
		t.Fatalf("selecting a history row did not open reason form: %T", d.modal)
	}
	return form
}

func submitReconsideration(t *testing.T, form *tview.Form) {
	t.Helper()
	form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), nil)
}

func TestDirectionHistoryIsProjectScopedAndOnlyShowsDismissedOrRejectedUnselectedRows(t *testing.T) {
	d, _, screen := reconsiderationDashboard(t)
	if len(d.directionRows) != 1 || d.directionRows[0].ID != "proposed-one" {
		t.Fatalf("normal direction panel changed its active rows: %+v", d.directionRows)
	}
	list := openDirectionHistory(t, d)
	if got := list.GetItemCount(); got != 2 {
		t.Fatalf("history contains %d rows, want dismissed/rejected current-project rows only", got)
	}
	d.ForceDraw()
	text := screenText(screen)
	for _, want := range []string{"Dismissed idea", "Rejected idea"} {
		if !strings.Contains(text, want) {
			t.Errorf("project history omitted %q: %s", want, text)
		}
	}
	for _, absent := range []string{"Foreign history", "Deferred idea", "Already selected", "Current idea"} {
		if strings.Contains(text, absent) {
			t.Errorf("history exposed ineligible row %q: %s", absent, text)
		}
	}
}

func TestDirectionReconsiderationSubmitsCapturedDirectionWithoutApproval(t *testing.T) {
	d, snapshot, _ := reconsiderationDashboard(t)
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return json.RawMessage(`{}`), nil
	})
	list := openDirectionHistory(t, d)
	form := reconsiderationForm(t, d, list)
	input, ok := form.GetFormItem(1).(*tview.InputField)
	if !ok {
		t.Fatalf("reason input missing from reconsideration form: %T", form.GetFormItem(1))
	}
	input.SetText("Evidence changed since rejection")

	snapshot["directions"][0].Version = 8
	snapshot["projects"] = []model.Record{record("project-two", model.Project{ID: "project-two", Name: "Other project"})}
	d.Present(snapshot)
	submitReconsideration(t, form)

	var request control.Request
	select {
	case request = <-requests:
	case <-time.After(time.Second):
		t.Fatal("reconsideration form did not submit a command")
	}
	if request.Operation != "direction.reconsider" || request.ProjectID != "project-one" || request.ResourceID != "dismissed-one" || request.ExpectedVersion != 7 {
		t.Fatalf("reconsideration did not retain its captured target and version: %+v", request)
	}
	if request.IdempotencyKey == "" || request.RequestID == "" {
		t.Fatalf("supervisor command lacks request identity: %+v", request)
	}
	var payload struct {
		ProjectID string `json:"project_id"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(request.Payload, &payload); err != nil || payload.ProjectID != "project-one" || payload.Reason != "Evidence changed since rejection" {
		t.Fatalf("wrong reconsideration payload %s: %v", request.Payload, err)
	}
}

func TestDirectionReconsiderationRejectsBlankReasonWithoutSending(t *testing.T) {
	d, _, _ := reconsiderationDashboard(t)
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return json.RawMessage(`{}`), nil
	})
	form := reconsiderationForm(t, d, openDirectionHistory(t, d))
	submitReconsideration(t, form)
	if d.modal != form {
		t.Fatal("blank reason closed the reconsideration form")
	}
	select {
	case request := <-requests:
		t.Fatalf("blank reason sent supervisor command: %+v", request)
	default:
	}
}
