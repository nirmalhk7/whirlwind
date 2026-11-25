package dashboard

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"whirlwind/internal/control"
)

func TestDashboardSurfacesDispatchBlockUntilRecoveryIsVerified(t *testing.T) {
	d := New(t.TempDir())
	screen := dashboardScreen(t, d)
	screen.SetSize(80, 24)
	const reason = "sandbox error: isolation could not be verified: registered standalone runtime identity is incomplete or invalid"
	snapshot := control.Snapshot{
		"supervisor":      {record("local", map[string]bool{"online": true})},
		"dispatch_health": {record("local", map[string]any{"checked": true, "block_reason": reason})},
	}
	present := func() {
		t.Helper()
		d.status.SetText(d.supervisorStatus(snapshot))
		d.Present(snapshot)
		d.ForceDraw()
	}
	present()
	if label := d.runnerStatus.GetText(false); label != "[BLOCK ]" {
		t.Fatalf("online supervisor advertised %q while dispatch was blocked", label)
	}
	shown := screenText(screen)
	if !strings.Contains(shown, "Dispatch blocked") || !strings.Contains(shown, "registered standalone runtime") {
		t.Fatalf("no actionable visible reason at 80×24:\n%s", shown)
	}
	d.input(tcell.NewEventKey(tcell.KeyRune, '!', tcell.ModNone))
	view, ok := d.modal.(*tview.TextView)
	if !ok || !strings.Contains(view.GetText(false), reason) {
		t.Fatal("the blocked status did not open the full recovery reason")
	}
	d.input(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	snapshot["dispatch_health"][0] = record("local", map[string]any{"checked": false})
	present()
	if label := d.runnerStatus.GetText(false); label == "[  OK  ]" {
		t.Fatal("unchecked recovery was treated as ready")
	}
	snapshot["dispatch_health"][0] = record("local", map[string]any{"checked": true})
	present()
	if d.runnerStatus.GetText(false) != "[  OK  ]" || strings.Contains(d.status.GetText(false), "blocked") {
		t.Fatalf("verified recovery did not restore readiness: %q %q", d.runnerStatus.GetText(false), d.status.GetText(false))
	}
}
