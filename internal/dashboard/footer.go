package dashboard

import (
	"encoding/json"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"whirlwind/internal/control"
)

const keyboardShortcuts = "Tab/Shift-Tab panels | Home/End first/last | PgUp/PgDn page | Enter open (Runs: logs) | i Run ideation (Projects) | v/click rationale (Inbox) | a choose direction / prepare plan (Inbox) | d delete (Inbox) | h rejected history (Inbox) | o earlier directions (Inbox) | t tasks (Inbox) | r plan review (approve / request changes / reject) | v Vim editor (plan review, plan view) | c questions | p permissions | f finalization | x stop/resume runners | j/k/g/G/h/l scroll (text) | Ctrl-B/Ctrl-F page (text) | Esc close | ? all keys | q/Ctrl-C quit"

// Resize the footer before laying out panels, keeping shortcuts visible on resize.
type dashboardLayout struct {
	*tview.Flex
	help *tview.TextView
}

func (layout *dashboardLayout) Draw(screen tcell.Screen) {
	_, _, width, _ := layout.GetRect()
	lines := shortcutLines(width)
	layout.help.SetText(strings.Join(lines, "\n"))
	layout.ResizeItem(layout.help, max(1, len(lines)), 0)
	layout.Flex.Draw(screen)
}

func shortcutLines(width int) []string {
	var lines []string
	line := ""
	for _, shortcut := range strings.Split(keyboardShortcuts, " | ") {
		if line != "" && tview.TaggedStringWidth(line+" | "+shortcut) > width {
			lines = append(lines, tview.WordWrap(line, width)...)
			line = ""
		}
		if line != "" {
			line += " | "
		}
		line += shortcut
	}
	return append(lines, tview.WordWrap(line, width)...)
}

func (d *Dashboard) updateRunnerStatus(snapshot control.Snapshot) {
	ready := supervisorOnline(snapshot)
	for _, record := range snapshot["settings"] {
		if record.ID != "global" {
			continue
		}
		var settings struct {
			Stopped bool `json:"stopped"`
		}
		if json.Unmarshal(record.Data, &settings) != nil || settings.Stopped {
			ready = false
		}
	}
	d.runnerStatus.SetText("[ DOWN ]").SetTextColor(tcell.ColorWhite).SetBackgroundColor(tcell.ColorRed)
	if ready {
		d.runnerStatus.SetText("[  OK  ]").SetTextColor(tcell.ColorBlack).SetBackgroundColor(tcell.ColorGreen)
	}
}

func supervisorOnline(snapshot control.Snapshot) bool {
	for _, record := range snapshot["supervisor"] {
		var value struct {
			Online bool `json:"online"`
		}
		if json.Unmarshal(record.Data, &value) == nil && value.Online {
			return true
		}
	}
	return false
}
