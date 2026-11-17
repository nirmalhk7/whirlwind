package dashboard

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"whirlwind/internal/control"
)

func TestRunsScrollbarTracksKeyboardSelection(t *testing.T) {
	d := New()
	screen := dashboardScreen(t, d)
	d.runs.Clear()
	for range 40 {
		d.runs.AddItem("run", "model", 0, nil)
	}
	d.ForceDraw()
	x, y, width, height := d.runs.GetInnerRect()
	assertThumb := func(row int) {
		t.Helper()
		char, _, _, _ := screen.GetContent(x+width, row)
		if char != '█' {
			t.Fatalf("missing scrollbar thumb at row %d: %q", row, char)
		}
	}
	assertThumb(y)
	d.runs.InputHandler()(tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	d.ForceDraw()
	assertThumb(y + height - 1)
}

func TestRunsScrollbarClickScrollsWithoutOpeningLogs(t *testing.T) {
	d := New()
	dashboardScreen(t, d)
	d.runs.Clear()
	for range 40 {
		d.runs.AddItem("run", "model", 0, nil)
	}
	opened := false
	d.runs.SetSelectedFunc(func(int, string, string, rune) { opened = true })
	d.ForceDraw()
	x, y, width, height := d.runs.GetInnerRect()
	d.root.MouseHandler()(tview.MouseLeftClick, tcell.NewEventMouse(x+width, y+height-1, tcell.Button1, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	if opened || d.runs.GetCurrentItem() != 39 {
		t.Fatalf("scrollbar click opened logs=%v, selected=%d; want last run", opened, d.runs.GetCurrentItem())
	}
	_, capture := d.root.MouseHandler()(tview.MouseLeftDown, tcell.NewEventMouse(x+width, y+height-1, tcell.Button1, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	if capture == nil {
		t.Fatal("scrollbar did not capture drag")
	}
	capture.MouseHandler()(tview.MouseMove, tcell.NewEventMouse(x+width, y, tcell.Button1, tcell.ModNone), func(p tview.Primitive) { d.SetFocus(p) })
	if d.runs.GetCurrentItem() != 0 || opened {
		t.Fatal("dragging scrollbar to top did not select first run without opening logs")
	}
}

func TestDashboardStatusBoxReflectsRunnerAvailability(t *testing.T) {
	d := New()
	screen := dashboardScreen(t, d)
	for _, test := range []struct {
		name            string
		online, stopped bool
		label           string
		color           tcell.Color
	}{
		{"enabled", true, false, "OK", tcell.ColorGreen},
		{"stopped", true, true, "DOWN", tcell.ColorRed},
		{"offline", false, false, "DOWN", tcell.ColorRed},
		{"resumed", true, false, "OK", tcell.ColorGreen},
	} {
		t.Run(test.name, func(t *testing.T) {
			d.Present(control.Snapshot{
				"supervisor": {record("local", map[string]bool{"online": test.online})},
				"settings":   {record("global", map[string]bool{"stopped": test.stopped})},
			})
			d.ForceDraw()
			_, row, _, _ := d.status.GetRect()
			width, _ := screen.Size()
			var box strings.Builder
			for col := width - 8; col < width; col++ {
				char, _, style, _ := screen.GetContent(col, row)
				box.WriteRune(char)
				fg, bg, _ := style.Decompose()
				if fg != test.color && bg != test.color {
					t.Errorf("status box lacks %s color at column %d", test.name, col)
				}
			}
			if !strings.Contains(box.String(), test.label) {
				t.Fatalf("status box = %q, want %s", box.String(), test.label)
			}
		})
	}
}

func TestFooterShowsEveryShortcutAcrossTerminalSizes(t *testing.T) {
	d := New()
	screen := dashboardScreen(t, d)
	for _, width := range []int{213, 160, 100, 60} {
		screen.SetSize(width, 50)
		d.ForceDraw()
		_, row, _, _ := d.status.GetRect()
		rows := strings.Split(screenText(screen), "\n")
		footer := strings.Join(strings.Fields(strings.Join(rows[row+1:], " ")), " ")
		for _, unwanted := range []string{"Up/Down", "Left/Right", "l logs"} {
			if strings.Contains(footer, unwanted) {
				t.Errorf("width %d footer advertises removed instruction %q", width, unwanted)
			}
		}
		for _, shortcut := range []string{"Shift-Tab", "Home/End", "PgUp/PgDn", "Enter", "Vim", "logs", "a choose direction / prepare plan", "d delete", "h rejected history", "o earlier directions", "t tasks", "r plan review (approve / request changes / reject)", "c questions", "p permissions", "f finalization", "x stop/resume", "Esc", "?", "q/Ctrl-C quit"} {
			if !strings.Contains(footer, shortcut) {
				t.Errorf("width %d footer missing %q", width, shortcut)
			}
		}
	}
}

func TestLDoesNotOpenPanelLogs(t *testing.T) {
	for _, panel := range []int{2, 3} {
		t.Run(string(rune('0'+panel)), func(t *testing.T) {
			d := New(t.TempDir())
			dashboardScreen(t, d)
			d.focus = panel
			d.SetFocus(d.focusedPanel())
			event := tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone)
			before := d.status.GetText(false)
			if got := d.input(event); got != event {
				t.Fatal("l was captured as a panel action instead of remaining available for navigation")
			}
			if d.modal != nil || d.status.GetText(false) != before {
				t.Fatal("l opened or attempted to load logs")
			}
		})
	}
}
