package dashboard

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func runList(d *Dashboard) *tview.List {
	d.focus = 2
	return d.focusedPanel().(*tview.List)
}

func TestRunsReplacePlansAndShowNewestAgentHistory(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(180, 60)
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repo"})},
		"tasks":    {record("t", model.Task{ID: "t", ProjectID: "p"})},
		"sessions": {
			record("old", model.Attempt{AttemptID: "old", ProjectID: "p", Role: "code", Provider: "codex", Model: "old-model", Status: "complete", Finished: true, StartedAt: "2026-11-08T09:00:00Z"}),
			record("latest", model.Attempt{AttemptID: "latest", ProjectID: "p", Role: "ideate", Provider: "cursor", ResolvedModel: "live-model", EffectiveThinking: &model.ThinkingChoice{Value: "high"}, Status: "running", StartedAt: "2026-11-08T12:00:00+02:00"}),
			record("failed", model.Attempt{AttemptID: "failed", Task: "t", Role: "review", Provider: "claude", Model: "review-model", Thinking: &model.ThinkingChoice{Value: "low"}, Status: "failed", StartedAt: "2026-11-08T09:30:00Z", Error: "provider disconnected"}),
			record("other", model.Attempt{AttemptID: "other", ProjectID: "other", Role: "plan", Provider: "hidden", Status: "running", StartedAt: "2026-11-09T10:00:00Z"}),
		},
	})
	d.ForceDraw()
	view := screenText(screen)
	if !strings.Contains(view, " Runs ") || strings.Contains(view, " Plans ") {
		t.Fatalf("Runs did not replace Plans: %s", view)
	}
	list := runList(d)
	if list.GetItemCount() != 3 {
		t.Fatalf("want 3 project runs, got %d", list.GetItemCount())
	}
	for i, want := range []string{"ideate", "review", "code"} {
		primary, secondary := list.GetItemText(i)
		if !strings.Contains(primary, want) {
			t.Errorf("row %d: %q", i, primary)
		}
		for _, field := range []string{[]string{"live-model", "review-model", "old-model"}[i], "2026-11-08"} {
			if !strings.Contains(secondary, field) {
				t.Errorf("run row missing %q: %s", field, secondary)
			}
		}
	}
	first, second := list.GetItemText(0)
	if !strings.Contains(first, "running") || !strings.Contains(second, "high") {
		t.Fatalf("missing effective run state: %s %s", first, second)
	}
	list.SetCurrentItem(1)
	for _, want := range []string{"review-model", "low", "failed", "provider disconnected", "2026-11-08T09:30:00Z"} {
		if !strings.Contains(d.detail.GetText(false), want) {
			t.Errorf("selected run detail missing %q", want)
		}
	}
	last, _ := list.GetItemText(2)
	if !strings.Contains(last, "completed") {
		t.Fatalf("completed run mislabeled: %s", last)
	}
}

func TestRunSelectionSurvivesRefreshAndNewerRuns(t *testing.T) {
	d := New()
	snapshot := control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repo"})},
		"sessions": {record("old", model.Attempt{AttemptID: "old", ProjectID: "p", Role: "code", StartedAt: "2026-11-08T09:00:00Z"}), record("new", model.Attempt{AttemptID: "new", ProjectID: "p", Role: "review", StartedAt: "2026-11-08T10:00:00Z"})},
	}
	d.Present(snapshot)
	list := runList(d)
	list.SetCurrentItem(1)
	snapshot["sessions"] = append(snapshot["sessions"], record("newest", model.Attempt{AttemptID: "newest", ProjectID: "p", Role: "plan", StartedAt: "2026-11-08T11:00:00Z"}))
	d.Present(snapshot)
	primary, _ := list.GetItemText(list.GetCurrentItem())
	if !strings.Contains(primary, "code") {
		t.Fatalf("refresh changed selected run: %q", primary)
	}
}

func TestProjectsHaveNoWhiteSelectionBackground(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(120, 35)
	d.Present(control.Snapshot{"projects": {record("p", model.Project{ID: "p", Name: "ProjectWithoutHighlight"})}})
	d.ForceDraw()
	cells, _, _ := screen.GetContents()
	for i := 0; i+len("ProjectWithoutHighlight") <= len(cells); i++ {
		var text strings.Builder
		for j := range len("ProjectWithoutHighlight") {
			text.WriteString(string(cells[i+j].Runes))
		}
		if text.String() == "ProjectWithoutHighlight" {
			_, background, _ := cells[i].Style.Decompose()
			if background == tcell.ColorWhite {
				t.Fatal("project selection still has white background")
			}
			return
		}
	}
	t.Fatal("project row not rendered")
}

func TestProjectRunWithoutTaskCannotActOnUnrelatedTask(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repo"})},
		"tasks":    {record("t", model.Task{ID: "t", ProjectID: "p"})},
		"sessions": {record("ideate", model.Attempt{AttemptID: "ideate", ProjectID: "p", Role: "ideate"})},
	})
	if _, _, ok := d.selectedPlanRecord(); ok {
		t.Fatal("project-level run selected unrelated task for decisions")
	}
	d.focus = 3
	d.input(tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModNone))
	if d.modal == nil {
		t.Fatal("Inbox task chooser unavailable")
	}
}

func TestInboxTaskInspectionRemainsSelectedAcrossRunRefresh(t *testing.T) {
	d := New()
	snapshot := control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repo"})},
		"tasks":    {record("first", model.Task{ID: "first", ProjectID: "p"}), record("second", model.Task{ID: "second", ProjectID: "p"})},
		"sessions": {record("run", model.Attempt{AttemptID: "run", ProjectID: "p", Task: "first", Role: "code"})},
	}
	d.Present(snapshot)
	d.focus = 3
	d.input(tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModNone))
	chooser := d.modal.(*tview.List)
	chooser.SetCurrentItem(1)
	chooser.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	d.Present(snapshot)
	task, _, ok := d.selectedPlanRecord()
	if !ok || task.ID != "second" {
		t.Fatalf("refresh switched Inbox task: %+v", task)
	}
}

func TestUnreadableRunsClearStaleHistory(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{"sessions": {record("old", model.Attempt{AttemptID: "old", Role: "code"})}})
	d.Present(control.Snapshot{"sessions": {{ID: "broken", Data: []byte("{")}}})
	primary, _ := runList(d).GetItemText(0)
	if !strings.Contains(primary, "Cannot read run history") {
		t.Fatalf("missing read failure: %q", primary)
	}
	if len(d.runRows) != 0 || d.runID != "" {
		t.Fatal("unreadable history retained stale selection")
	}
}
