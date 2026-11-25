package dashboard

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func TestRunsShowsAutomaticIdeationTriggerAndHold(t *testing.T) {
	d := New(t.TempDir())
	screen := dashboardScreen(t, d)
	screen.SetSize(80, 24)
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Project", Status: "error"})},
		"operations": {record("auto", model.Operation{
			Kind: "ideation", Subject: "p", Stage: "queued", Error: "project setup failed; retry setup or start ideation manually",
			Input: map[string]string{"trigger": "project_registration", "project_id": "p", "day": "2026-11-24"},
		})},
	})
	d.ForceDraw()
	if !strings.Contains(screenText(screen), "Auto ideation") {
		t.Fatal("80×24 Runs hid the automatic trigger")
	}
	for range 2 {
		d.input(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	}
	d.input(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	view, ok := d.modal.(*tview.TextView)
	if !ok || !strings.Contains(view.GetText(false), "Trigger: project registration (automatic)") ||
		!strings.Contains(view.GetText(false), "Not started: project setup failed") {
		t.Fatal("Runs detail did not explain the automatic trigger and why no attempt started")
	}
}
