package dashboard

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestDashboardRendersInboxPane(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(140, 45)
	d.ForceDraw()
	if !strings.Contains(screenText(screen), " Inbox ") {
		t.Fatal("operator cannot find Inbox pane")
	}
	if strings.Contains(screenText(screen), " Directions ") || strings.Contains(screenText(screen), "No directions") {
		t.Fatal("Directions box still occupies Inbox")
	}
}
