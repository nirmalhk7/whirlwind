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
)

func TestRunnerToggleKeySubmitsNamedCommand(t *testing.T) {
	d := New()
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return json.RawMessage(`{}`), nil
	})
	if event := d.input(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)); event != nil {
		t.Fatal("x was not handled as runner toggle")
	}
	select {
	case request := <-requests:
		if request.Operation != "runners.toggle" || request.IdempotencyKey == "" || request.RequestID == "" {
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
