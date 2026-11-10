package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func TestCyanFrameFollowsFocusedPanel(t *testing.T) {
	d := New()
	screen := dashboardScreen(t, d)
	panels := []*tview.Box{d.projects.Box, d.agents.Box, d.runs.Box, d.inbox.Box}
	check := func(selected int) {
		t.Helper()
		d.ForceDraw()
		for i, panel := range panels {
			x, y, _, _ := panel.GetRect()
			_, _, style, _ := screen.GetContent(x, y)
			foreground, _, _ := style.Decompose()
			want := tcell.ColorWhite
			if i == selected {
				want = tcell.ColorAqua
			}
			if foreground != want {
				t.Fatalf("focused %d, panel %d frame: got %v, want %v", selected, i, foreground, want)
			}
		}
	}
	check(0)
	for _, selected := range []int{1, 2, 3, 0} {
		d.input(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
		check(selected)
	}
	d.input(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone))
	check(3)
	x, y, _, _ := d.projects.GetInnerRect()
	d.projects.MouseHandler()(tview.MouseLeftClick, tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	check(0)
}

func TestInboxPrefixesSavedTimestampsForEveryMessageKind(t *testing.T) {
	stamp := time.Date(2026, 11, 9, 14, 20, 0, 0, time.Local).Format(time.RFC3339)
	first := time.Date(2026, 11, 9, 13, 10, 0, 0, time.Local).Format(time.RFC3339)
	idea := record("idea", model.Direction{ID: "idea", ProjectID: "p", Title: "Timestamped idea"})
	idea.UpdatedAt = stamp
	notice := record("notice", model.Notification{ID: "notice", Role: "review", TaskID: "task", Message: "Recorded sender", FirstAt: first, LastAt: stamp})
	notice.UpdatedAt = stamp
	legacy := record("legacy", model.Notification{ID: "legacy", TaskID: "task", Kind: "question", Message: "Agent clarification awaits answer: q"})
	legacy.UpdatedAt = stamp
	d := New()
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p"})},
		"directions": {idea},
		"tasks":      {record("task", model.Task{ID: "task", ProjectID: "p"})},
		"sessions":   {record("attempt", model.Attempt{AttemptID: "attempt", Task: "task", ProjectID: "p", Role: "code"})},
		"questions":  {record("q", model.QuestionSet{ID: "q", TaskID: "task", AttemptID: "attempt"})},
		"inbox":      {notice, legacy},
	})
	for i, want := range []string{"2026-11-09 14:20 · Ideation - Timestamped idea", "2026-11-09 13:10 · Reviewer - Recorded sender", "2026-11-09 14:20 · Coding - Agent clarification awaits answer: q"} {
		got, _ := d.inbox.GetItemText(i)
		if got != want {
			t.Errorf("Inbox item %d: %q, want %q", i, got, want)
		}
	}
}

func TestInboxMissingTimestampsStayExplicitlyUnknown(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{"directions": {record("idea", model.Direction{ID: "idea", Title: "Undated proposal"})}})
	got, _ := d.inbox.GetItemText(0)
	if !strings.HasPrefix(got, "time unknown · ") {
		t.Fatalf("missing saved timestamp replaced with misleading date: %q", got)
	}
}
