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

func TestRunnerToggleKeySubmitsNamedCommand(t *testing.T) {
	d := New()
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return json.RawMessage(`{}`), nil
	})
	d.Present(control.Snapshot{"settings": {record("global", map[string]bool{"stopped": false})}})
	if event := d.input(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)); event != nil {
		t.Fatal("x was not handled as runner toggle")
	}
	select {
	case request := <-requests:
		if request.Operation != "runners.toggle" || request.IdempotencyKey == "" || request.RequestID == "" || request.ExpectedVersion != 1 || request.ResourceID != "global" || !strings.Contains(string(request.Payload), `"stopped":true`) {
			t.Fatalf("toggle lacks named operation or retry identity: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("x did not send runner toggle")
	}
}

func TestRunnerToggleKeyPreservesModalTyping(t *testing.T) {
	d := New()
	d.showForm(tview.NewForm().AddInputField("Answer", "", 10, nil, nil))
	event := tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)
	if d.input(event) != event {
		t.Fatal("typing x in form triggered runner toggle")
	}
}

func TestRunnerToggleWithoutSupervisorReportsUnavailable(t *testing.T) {
	d := New()
	d.input(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if !strings.Contains(d.status.GetText(false), "unavailable") {
		t.Fatalf("missing offline feedback: %s", d.status.GetText(false))
	}
}

func TestProjectIdeationKeyStartsFreshRunForSelectedProject(t *testing.T) {
	d := New()
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return json.RawMessage(`{}`), nil
	})
	d.Present(control.Snapshot{"projects": {
		record("other", model.Project{ID: "other", Name: "Other"}),
		record("basera", model.Project{ID: "basera", Name: "Basera"}),
	}})
	d.projects.SetCurrentItem(1)
	d.selectProject(1, "", "", 0)
	d.focus = 0

	if event := d.input(tcell.NewEventKey(tcell.KeyRune, 'i', tcell.ModNone)); event != nil {
		t.Fatal("i was not handled in Projects")
	}
	select {
	case request := <-requests:
		var payload struct {
			Fresh bool `json:"fresh"`
		}
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if request.Operation != "ideation.start" || request.ProjectID != "basera" || request.ExpectedVersion != 1 || !payload.Fresh {
			t.Fatalf("manual run was not scoped as a fresh selected-project request: %+v payload=%s", request, request.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("i did not start project ideation")
	}
}

func TestProjectIdeationDoesNotChangeStoppedRunnerState(t *testing.T) {
	d := New()
	called := false
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		called = true
		return json.RawMessage(`{}`), nil
	})
	d.Present(control.Snapshot{
		"projects":   {record("basera", model.Project{ID: "basera", Name: "Basera"})},
		"supervisor": {record("local", map[string]bool{"online": true})},
		"settings":   {record("global", map[string]bool{"stopped": true})},
	})
	d.focus = 0
	d.input(tcell.NewEventKey(tcell.KeyRune, 'i', tcell.ModNone))
	if called {
		t.Fatal("manual ideation silently submitted while runners were stopped")
	}
	if status := d.status.GetText(false); !strings.Contains(status, "runners stopped") || !strings.Contains(status, "press x") {
		t.Fatalf("stopped-runner response lacks truthful resume instruction: %s", status)
	}
}
