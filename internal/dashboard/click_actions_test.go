package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func dashboardScreen(t *testing.T, d *Dashboard) tcell.SimulationScreen {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(160, 50)
	return screen
}

func TestAgentRowsHaveNoBlankLines(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{"providers": {record("a", model.Provider{ID: "a", Name: "First"}), record("b", model.Provider{ID: "b", Name: "Second"})}})
	if strings.Contains(d.agents.GetText(false), "\n\n") {
		t.Fatal("blank row between agents")
	}
}

func TestSelectedRunHighlightsBothLinesAfterScrolling(t *testing.T) {
	d := New()
	screen := dashboardScreen(t, d)
	snapshot := control.Snapshot{}
	for i := 0; i < 20; i++ {
		snapshot["sessions"] = append(snapshot["sessions"], record(store.ID(), model.Attempt{Role: "ideate", Model: "model"}))
	}
	d.Present(snapshot)
	d.runs.SetCurrentItem(19)
	d.ForceDraw()
	offset, _ := d.runs.GetOffset()
	x, y, w, _ := d.runs.GetInnerRect()
	y += 2 * (19 - offset)
	for column := 0; column < w; column++ {
		_, _, first, _ := screen.GetContent(x+column, y)
		_, _, second, _ := screen.GetContent(x+column, y+1)
		_, bg1, _ := first.Decompose()
		_, bg2, _ := second.Decompose()
		if bg1 != tcell.ColorWhite || bg2 != bg1 {
			t.Fatalf("column %d selection backgrounds: %v / %v", column, bg1, bg2)
		}
	}
}

func TestInboxClickAndVShowDirectionRationaleWithoutDecision(t *testing.T) {
	d := New()
	dashboardScreen(t, d)
	calls := 0
	d.SetCommandHandler(func(_ context.Context, _ control.Request) (json.RawMessage, error) {
		calls++
		return json.RawMessage("{}"), nil
	})
	d.Present(control.Snapshot{"projects": {record("p", model.Project{ID: "p"})}, "directions": {record("idea", model.Direction{ID: "idea", ProjectID: "p", GroupID: "current", Day: "2026-11-10", Title: "Idea", Problem: "Underlying problem", Mechanism: "Proposed mechanism", ExpectedBenefit: "Fewer review cycles"})}})
	d.focus = 3
	d.ForceDraw()
	x, y, _, _ := d.inbox.GetInnerRect()
	d.inbox.MouseHandler()(tview.MouseLeftClick, tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	assertRationale := func() {
		t.Helper()
		view, ok := d.modal.(*tview.TextView)
		if !ok {
			t.Fatalf("rationale action opened %T, want read-only rationale", d.modal)
		}
		for _, want := range []string{"Problem", "Underlying problem", "Approach", "Proposed mechanism", "Benefit", "Fewer review cycles"} {
			if !strings.Contains(view.GetText(false), want) {
				t.Errorf("rationale view omitted %q", want)
			}
		}
	}
	assertRationale()
	if calls != 0 {
		t.Fatal("clicking to view rationale submitted a direction decision")
	}
	d.modalInput(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	d.focus = 3
	d.input(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	assertRationale()
	d.input(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	if _, ok := d.modal.(*tview.List); !ok {
		t.Fatalf("a did not open the explicit direction chooser: %T", d.modal)
	}
	if calls != 0 {
		t.Fatal("opening the direction chooser submitted a decision")
	}
}

func TestAllKeyboardInstructionsCanOpenAndClose(t *testing.T) {
	d := New()
	d.input(tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone))
	view, ok := d.modal.(*tview.TextView)
	if !ok {
		t.Fatal("? did not open keyboard instructions")
	}
	for _, key := range []string{"Shift-Tab", "Vim", "logs", "review", "permissions", "stop/resume"} {
		if !strings.Contains(view.GetText(false), key) {
			t.Errorf("help missing %s", key)
		}
	}
	d.input(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if d.modal != nil {
		t.Fatal("Esc did not close instructions")
	}
}

func TestFooterKeepsKeyboardHelpAfterStatusUpdates(t *testing.T) {
	d := New()
	screen := dashboardScreen(t, d)
	d.status.SetText(d.supervisorStatus(control.Snapshot{"supervisor": {record("local", map[string]bool{"online": true})}}))
	d.ForceDraw()
	rows := strings.Split(strings.TrimSuffix(screenText(screen), "\n"), "\n")
	_, statusRow, _, _ := d.status.GetRect()
	footer := strings.Join(rows[statusRow:], "\n")
	for _, want := range []string{"connected", "Tab", "quit", "v/click rationale", "logs", "approve"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer missing %s: %s", want, footer)
		}
	}
}

func TestRunClickOrEnterShowsOnlyThatRunsFullSavedLogs(t *testing.T) {
	snapshot := seedSavedRunLogs(t)
	for _, line := range []int{0, 1, 2} {
		t.Run(string(rune('0'+line)), func(t *testing.T) {
			capture := captureRunTail(t, "first FINAL")
			d := New()
			screen := tcell.NewSimulationScreen("UTF-8")
			d.SetScreen(screen)
			screen.SetSize(160, 50)
			d.Present(snapshot)
			done := make(chan error, 1)
			go func() { done <- d.Run() }()
			defer func() { d.Stop(); <-done }()
			d.QueueUpdateDraw(func() {
				if line == 2 {
					d.runs.SetCurrentItem(1)
					d.SetFocus(d.runs)
					d.input(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
					return
				}
				x, y, _, _ := d.runs.GetInnerRect()
				d.runs.MouseHandler()(tview.MouseLeftClick, tcell.NewEventMouse(x, y+2+line, tcell.Button1, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
			})
			body, err := os.ReadFile(capture)
			if err != nil {
				t.Fatalf("selected run did not open in terminal tail: %v", err)
			}
			text := string(body)
			if !strings.Contains(text, "first prompt") || !strings.Contains(text, "first FINAL") || strings.Contains(text, "second FINAL") {
				t.Fatal("terminal tail mixed runs or omitted full history")
			}
			if d.modal != nil {
				t.Fatal("run logs still opened a tview modal")
			}

		})
	}
}

func seedSavedRunLogs(t *testing.T) control.Snapshot {
	t.Helper()
	directory := t.TempDir()
	t.Setenv("WHIRLWIND_DATA_DIR", directory)
	database, err := store.Open(filepath.Join(directory, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snapshot := control.Snapshot{}
	for _, id := range []string{"first", "second"} {
		attempt := model.Attempt{AttemptID: id, Role: "ideate", Model: id, StartedAt: "2026-11-09T10:00:00Z"}
		if err := database.Put(ctx, "sessions", id, attempt, 0); err != nil {
			t.Fatal(err)
		}
		snapshot["sessions"] = append(snapshot["sessions"], record(id, attempt))
		for _, body := range []string{id + " prompt", strings.Repeat(id+" log\n", 80000) + id + " FINAL"} {
			if _, err := database.Capture(ctx, id, "output", "text/plain", []byte(body), store.AttemptPayloadLimit, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestInboxSelectionHighlightsFullWidth(t *testing.T) {
	d := New()
	screen := dashboardScreen(t, d)
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p"})},
		"directions": {record("idea", model.Direction{ID: "idea", ProjectID: "p", Title: "Short idea"})},
	})
	d.ForceDraw()
	x, y, width, _ := d.inbox.GetInnerRect()
	for column := 0; column < width; column++ {
		_, _, style, _ := screen.GetContent(x+column, y)
		_, background, _ := style.Decompose()
		if background != tcell.ColorWhite {
			t.Fatalf("Inbox selection ends at column %d of %d: background %v", column, width, background)
		}
	}
}
