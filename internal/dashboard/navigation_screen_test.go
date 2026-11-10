package dashboard

import (
	"github.com/gdamore/tcell/v2"
	"strings"
	"testing"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func TestDashboardOpensProjectAndPlanNavigation(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	app := New().SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(120, 32)
	app.ForceDraw()
	text := screenText(screen)
	for _, expected := range []string{"Whirlwind", "Projects", "Runs", "No projects", "Tab", "quit"} {
		if !strings.Contains(text, expected) {
			t.Errorf("dashboard missing %q:\n%s", expected, text)
		}
	}
}

func TestRefreshPreservesSelectedProjectAndPlan(t *testing.T) {
	d := New()
	snapshot := control.Snapshot{
		"projects": {record("first", model.Project{ID: "first", Name: "First"}), record("second", model.Project{ID: "second", Name: "Second"})},
		"tasks":    {record("one", model.Task{ID: "one", ProjectID: "second", Plan: &model.Plan{Objective: "One"}}), record("two", model.Task{ID: "two", ProjectID: "second", Plan: &model.Plan{Objective: "Two"}})},
	}
	d.Present(snapshot)
	d.projects.SetCurrentItem(1)
	d.plans.SetCurrentItem(1)
	d.Present(snapshot)
	project, _ := d.projects.GetItemText(d.projects.GetCurrentItem())
	plan, _ := d.plans.GetItemText(d.plans.GetCurrentItem())
	if project != "Second" || !strings.Contains(plan, "Two") {
		t.Fatalf("refresh lost selection: %q / %q", project, plan)
	}
}
