package dashboard

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func TestConfiguredProjectPathsRenderGitHealthAndInvalidEntries(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(160, 50)
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Valid repository", Path: "/work/valid"})},
		"project_status": {record("configured", []model.ProjectStatus{
			{ProjectID: "p", Path: "/work/valid", Status: "OK"},
			{Path: "/work/missing", Status: "ERR", Detail: "not a Git repository"},
		})},
	})
	d.ForceDraw()
	for _, want := range []string{"Valid repository", "missing", "OK", "ERR"} {
		if !strings.Contains(screenText(screen), want) {
			t.Errorf("project pane omitted %q", want)
		}
	}
	assertStatusColor(t, screen, "OK", tcell.ColorGreen)
	assertStatusColor(t, screen, "ERR", tcell.ColorRed)
	d.projects.SetCurrentItem(1)
	if !strings.Contains(d.detail.GetText(false), "not a Git repository") {
		t.Fatal("invalid path selection did not explain onboarding failure")
	}
}
