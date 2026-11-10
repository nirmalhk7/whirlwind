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
	"whirlwind/internal/workflow"
)

func TestDeleteKeyTargetsHighlightedInboxProposal(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{"projects": {record("p", model.Project{ID: "p"})}, "directions": {
		record("a", model.Direction{ID: "a", ProjectID: "p", Title: "Keep"}),
		record("b", model.Direction{ID: "b", ProjectID: "p", Title: "Delete"}),
	}})
	d.snapshot["directions"][1].Version = 7
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, r control.Request) (json.RawMessage, error) {
		requests <- r
		return json.RawMessage(`{"deleted":1}`), nil
	})
	d.SetFocus(d.inbox)
	d.inbox.SetCurrentItem(1)
	if d.input(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone)) != nil {
		t.Fatal("d was not handled")
	}
	select {
	case r := <-requests:
		if r.Operation != "direction.delete" || r.ResourceID != "b" || r.ProjectID != "p" || r.ExpectedVersion != 7 || r.IdempotencyKey == "" {
			t.Fatalf("wrong deletion authority: %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("d did not submit selected proposal deletion")
	}
}

func TestDeleteKeyDoesNotDeleteTaskNotice(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{"tasks": {record("task", model.Task{ID: "task"})}, "inbox": {record("notice", model.Notification{ID: "notice", TaskID: "task", Role: "plan", Message: "Review this plan"})}})
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, r control.Request) (json.RawMessage, error) { requests <- r; return nil, nil })
	d.SetFocus(d.inbox)
	d.input(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone))
	select {
	case r := <-requests:
		t.Fatalf("task notice deleted as proposal: %+v", r)
	default:
	}
	if !strings.Contains(d.status.GetText(false), "proposal") {
		t.Fatal("d did not explain its proposal-only scope")
	}
}

func TestAgentPausedLabelExplainsGlobalRunnerStop(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{"providers": {record("codex", model.Provider{ID: "codex", Name: "Codex"})}, "provider_status": {record("codex", workflow.ProviderStatus{ID: "codex", Status: "paused", Detail: "Command installed. Connection checks paused while runners are stopped."})}})
	if !strings.Contains(d.agents.GetText(false), "runners stopped") {
		t.Fatalf("paused reason hidden: %s", d.agents.GetText(false))
	}
}
