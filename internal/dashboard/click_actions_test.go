package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestInboxClickOpensEntireMessageInVim(t *testing.T) {
	directory := t.TempDir()
	capture := filepath.Join(directory, "captured")
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VIM_CAPTURE", capture)
	if err := os.WriteFile(filepath.Join(directory, "vim"), []byte("#!/bin/sh\nfor arg do file=\"$arg\"; done\n/bin/cat \"$file\" > \"$VIM_CAPTURE\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	d := New()
	dashboardScreen(t, d)
	description := "First line\n" + strings.Repeat("full message [literal] ", 200) + "\nLast line"
	d.Present(control.Snapshot{"projects": {record("p", model.Project{ID: "p"})}, "directions": {record("idea", model.Direction{ID: "idea", ProjectID: "p", Title: "Idea", Description: description, Problem: "Underlying problem", Mechanism: "Proposed mechanism"})}})
	d.ForceDraw()
	x, y, _, _ := d.inbox.GetInnerRect()
	d.inbox.MouseHandler()(tview.MouseLeftClick, tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	body, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("Vim did not receive message: %v", err)
	}
	for _, want := range []string{description, "Underlying problem", "Proposed mechanism"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("Vim missing full message field %q", want)
		}
	}
	if d.modal != nil {
		t.Fatal("click opened approval instead of Vim")
	}
	if err := os.Remove(capture); err != nil {
		t.Fatal(err)
	}
	d.input(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	body, err = os.ReadFile(capture)
	if err != nil || !strings.Contains(string(body), description) {
		t.Fatal("v did not open same full message in Vim")
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
	footer := strings.Join(rows[len(rows)-2:], "\n")
	for _, want := range []string{"connected", "Tab", "quit", "Vim", "logs", "approve"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer missing %s: %s", want, footer)
		}
	}
}

func TestRunClickOrEnterShowsOnlyThatRunsFullSavedLogs(t *testing.T) {
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
	for _, line := range []int{0, 1, 2} {
		t.Run(string(rune('0'+line)), func(t *testing.T) {
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
			deadline := time.Now().Add(3 * time.Second)
			for {
				text := ""
				d.QueueUpdateDraw(func() {
					if view, ok := d.modal.(*tview.TextView); ok {
						text = view.GetText(false)
					}
				})
				if strings.Contains(text, "first FINAL") {
					if !strings.Contains(text, "first prompt") || strings.Contains(text, "second FINAL") {
						t.Fatal("logs mixed runs or omitted earlier event")
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("full selected-run logs not shown: %.200s", text)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
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
