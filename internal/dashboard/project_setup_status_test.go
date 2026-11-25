package dashboard

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

const (
	setupFixtureErrorBadge          = "Setup ERR"
	setupFixtureMostRecentOperation = "latest-setup"
)

func TestFailedProjectSetupIsVisibleAndRecoverableFromMainDashboard(t *testing.T) {
	d := New(t.TempDir())
	screen := dashboardScreen(t, d)
	screen.SetSize(80, 24)
	const reason = "Scanner unavailable; install it before retrying"
	project := model.Project{ID: "basera", Name: "basera", Path: "/work/basera", Status: "error"}
	snapshot := control.Snapshot{
		"projects": {
			record("basera", project),
			record("other", model.Project{ID: "other", Name: "Another repo", Status: "approved"}),
		},
		"project_status": {record("configured", []model.ProjectStatus{
			{ProjectID: "basera", Path: project.Path, Status: "OK"},
			{ProjectID: "other", Path: "/work/other", Status: "OK"},
		})},
		"operations": {
			record(setupFixtureMostRecentOperation, model.Operation{Kind: "project_setup", Subject: "basera", Stage: "failed", Error: reason, StartedAt: "2026-11-24T09:00:00Z", Input: map[string]bool{"onboarding": true}}),
			record("older-setup", model.Operation{Kind: "project_setup", Subject: "basera", Stage: "failed", Error: "outdated setup failure", StartedAt: "2026-11-24T08:00:00Z"}),
			record("other-setup", model.Operation{Kind: "project_setup", Subject: "other", Stage: "failed", Error: "another project's failure", StartedAt: "2026-11-24T10:00:00Z"}),
		},
	}
	d.Present(snapshot)
	d.ForceDraw()
	assertFailedSetupSummary(t, d, screenText(screen), reason)
	assertFailedSetupNoticeOpens(t, d, reason)
	d.projects.SetCurrentItem(1)
	if inbox, _ := d.inbox.GetItemText(0); inbox != "No inbox items" {
		t.Errorf("other project inherited a setup failure notice: %q", inbox)
	}
	d.projects.SetCurrentItem(0)
	project.Status = "draft"
	snapshot["projects"][0] = record("basera", project)
	d.Present(snapshot)
	if inbox, _ := d.inbox.GetItemText(0); inbox != "No inbox items" {
		t.Errorf("stale failure notice persisted after retry: %q", inbox)
	}
	if label, _ := d.projects.GetItemText(0); strings.Contains(label, setupFixtureErrorBadge) || !strings.Contains(label, "Git OK") {
		t.Errorf("retry did not clear setup failure without changing path health: %q", label)
	}
}

func assertFailedSetupSummary(t *testing.T, d *Dashboard, view, reason string) {
	t.Helper()
	for _, marker := range []string{setupFixtureErrorBadge, "Setup failed", "Scanner unavailable"} {
		if !strings.Contains(view, marker) {
			t.Errorf("main dashboard did not show %q:\n%s", marker, view)
		}
	}
	if strings.Contains(view, "outdated setup failure") || strings.Contains(view, "another project's failure") {
		t.Fatalf("dashboard attributed another failure to this project's current setup:\n%s", view)
	}

	label, _ := d.projects.GetItemText(0)
	if !strings.Contains(label, setupFixtureErrorBadge) || strings.Contains(label, "Git OK") {
		t.Errorf("failed setup was shown as Git health: %q", label)
	}
	run, _ := d.runs.GetItemText(0)
	if !strings.HasPrefix(run, "Setup failed:") {
		t.Errorf("Runs hid the terminal setup outcome behind its phase: %q", run)
	}
	inbox, _ := d.inbox.GetItemText(0)
	if !strings.Contains(inbox, reason) || !strings.Contains(inbox, "Git path: OK") {
		t.Errorf("Inbox did not distinguish the failed setup from Git path validity: %q", inbox)
	}
	if d.inbox.GetItemCount() != 1 {
		t.Errorf("setup should produce one current notice; got %d", d.inbox.GetItemCount())
	}

}

func assertFailedSetupNoticeOpens(t *testing.T, d *Dashboard, reason string) {
	t.Helper()
	d.SetFocus(d.inbox)
	d.inbox.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {
		// This fixture drives list selection without a tview focus dispatcher.
	})
	viewer, ok := d.modal.(*tview.TextView)
	if !ok {
		t.Fatal("Enter on the Inbox setup notice did not open the setup run")
	}
	for _, marker := range []string{setupFixtureMostRecentOperation, reason, "Press r in Runs"} {
		if !strings.Contains(viewer.GetText(false), marker) {
			t.Errorf("opened setup detail did not show %q: %s", marker, viewer.GetText(false))
		}
	}
	d.input(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	d.viewMessage(0)
	if view, ok := d.modal.(*tview.TextView); !ok || !strings.Contains(view.GetText(false), setupFixtureMostRecentOperation) {
		t.Fatal("opening the setup notice as a message did not select the current failed setup")
	}
	d.input(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))

}

func TestFailedProjectSetupShowsFallbackWithoutRetainedOperation(t *testing.T) {
	d := New(t.TempDir())
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repository", Status: "error"})},
	})
	label, _ := d.inbox.GetItemText(0)
	if !strings.Contains(label, "Setup failed") || strings.Contains(label, "Git path") {
		t.Fatalf("fallback hid the setup error or invented Git health: %q", label)
	}
	d.openInboxSelection(0)
	if view, ok := d.modal.(*tview.TextView); !ok || !strings.Contains(view.GetText(false), "No failed setup operation is retained") {
		t.Fatal("missing setup evidence did not produce actionable details")
	}
}
