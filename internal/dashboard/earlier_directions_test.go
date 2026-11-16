package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

const (
	earlierCurrentTitle = "Current proposal"
	earlierCurrentGroup = "current-group"
	earlierOlderTitle   = "Earlier proposal"
	earlierOlderGroup   = "older-group"
	earlierOlderTime    = "2026-11-10T09:00:00Z"
	earlierCurrentTime  = "2026-11-10T10:00:00Z"
)

func TestEarlierDirectionsModalBrowsesPriorGroupsAndMarksLegacyUnknown(t *testing.T) {
	current := generationDirection("current", earlierCurrentGroup, "expand", earlierCurrentTitle)
	older := generationDirection("older", earlierOlderGroup, "harden", earlierOlderTitle)
	older.Problem = "A real earlier problem"
	older.Mechanism = "A concrete earlier approach"
	older.ExpectedBenefit = "A measurable earlier benefit"
	older.Evidence = []model.EvidenceRef{{Kind: "code", Ref: "internal/worker.go", Summary: "Recorded evidence"}}
	legacy := generationDirection("legacy", "", "simplify", "Legacy proposal")
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(120, 36)
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repo"})},
		"directions": {
			generationRecord(1, "2026-11-10T08:00:00Z", legacy),
			generationRecord(2, earlierOlderTime, older),
			generationRecord(3, earlierCurrentTime, current),
		},
	})
	d.focus = 3
	d.input(tcell.NewEventKey(tcell.KeyRune, 'o', tcell.ModNone))
	d.ForceDraw()
	text := screenText(screen)
	if d.modal == nil || !strings.Contains(text, "Earlier generation") || !strings.Contains(text, earlierOlderTitle) || !strings.Contains(text, "Legacy") || strings.Contains(text, earlierCurrentTitle) {
		t.Fatalf("history browse is not older-only or explicit about legacy generation:\n%s", text)
	}
	for _, want := range []string{"generation unknown", "Problem", "Approach", "Benefit", "internal/worker.go"} {
		if !strings.Contains(text, want) {
			t.Errorf("read-only earlier details omitted %q:\n%s", want, text)
		}
	}
	if _, ok := d.modal.(*tview.List); !ok {
		t.Fatalf("earlier directions modal is not visible: %T", d.modal)
	}
}

func TestEarlierDirectionsBrowsingDoesNotSubmitAndEscapeReturnsToInbox(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repo"})},
		"directions": {
			generationRecord(1, earlierOlderTime, generationDirection("older", earlierOlderGroup, "expand", "Earlier")),
			generationRecord(2, earlierCurrentTime, generationDirection("current", earlierCurrentGroup, "expand", "Current")),
		},
	})
	calls := 0
	d.SetCommandHandler(func(_ context.Context, _ control.Request) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{}`), nil
	})
	d.focus = 3
	d.input(tcell.NewEventKey(tcell.KeyRune, 'o', tcell.ModNone))
	if d.modal == nil || calls != 0 {
		t.Fatal("opening direction history was not a read-only browse")
	}
	d.input(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if d.modal != nil || d.root == nil {
		t.Fatal("Escape did not close history and restore the dashboard")
	}
	if calls != 0 {
		t.Fatalf("browsing submitted %d commands", calls)
	}
}

func TestChoosingEarlierProposalUsesCapturedDirectionAndVersion(t *testing.T) {
	capture := decisionVim(t, "approve", "")
	d := New()
	dashboardScreen(t, d)
	requests := make(chan control.Request, 1)
	d.SetCommandHandler(func(_ context.Context, request control.Request) (json.RawMessage, error) {
		requests <- request
		return json.RawMessage(`{}`), nil
	})
	older := generationDirection("older-id", earlierOlderGroup, "harden", earlierOlderTitle)
	older.Problem, older.Mechanism, older.ExpectedBenefit = "Earlier problem", "Earlier approach", "Earlier benefit"
	current := generationDirection("current-id", earlierCurrentGroup, "expand", earlierCurrentTitle)
	olderRecord := generationRecord(1, earlierOlderTime, older)
	olderRecord.Version = 7
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repo"})},
		"directions": {
			olderRecord,
			generationRecord(2, earlierCurrentTime, current),
		},
	})
	d.focus = 3
	d.input(tcell.NewEventKey(tcell.KeyRune, 'o', tcell.ModNone))
	list, ok := d.modal.(*tview.List)
	if !ok {
		t.Fatalf("history selector type = %T, want proposal list", d.modal)
	}
	index := list.GetCurrentItem()
	list.SetCurrentItem(index)
	list.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), nil)
	body, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("choosing earlier proposal did not open decision editor: %v", err)
	}
	if !strings.Contains(string(body), earlierOlderTitle) || !strings.Contains(string(body), "Earlier problem") || strings.Contains(string(body), earlierCurrentTitle) {
		t.Fatalf("decision editor did not retain the captured earlier proposal:\n%s", body)
	}
	select {
	case request := <-requests:
		if request.Operation != "direction.approve" || request.ResourceID != "older-id" || request.ExpectedVersion != 7 {
			t.Fatalf("earlier choice used the wrong proposal authority: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatalf("approval was not submitted from the captured earlier proposal: %s", d.status.GetText(false))
	}
}
