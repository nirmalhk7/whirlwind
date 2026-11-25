package dashboard

import (
	"testing"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func TestCheckoutAliasKeepsSelectionAndRunHistory(t *testing.T) {
	d := New(t.TempDir())
	snapshot := control.Snapshot{
		"projects": {
			record("another", model.Project{ID: "another", Name: "Other checkout"}),
			record("old", model.Project{ID: "old", Name: "Local checkout"}),
			record("hosted", model.Project{ID: "hosted", Name: "Configured checkout"}),
		},
		"operations": {
			record("prior-setup", model.Operation{Subject: "old", Kind: "project_setup", Stage: "failed", StartedAt: "2026-11-24T09:00:00Z"}),
			record("current-run", model.Operation{Subject: "hosted", Kind: "ideation", Stage: "complete", StartedAt: "2026-11-24T10:00:00Z"}),
		},
		"sessions": {
			record("prior-session", model.Attempt{ProjectID: "old", Role: "ideate", StartedAt: "2026-11-24T08:00:00Z"}),
		},
	}
	d.Present(snapshot)
	d.projects.SetCurrentItem(1)
	d.selectProject(1, "", "", 0)
	if d.projectID != "old" || d.runID != "prior-setup" {
		t.Fatalf("fixture selection wrong: project=%s run=%s", d.projectID, d.runID)
	}
	snapshot["projects"] = []model.Record{
		snapshot["projects"][0], record("hosted", model.Project{ID: "hosted", Name: "Configured checkout", CheckoutAliases: []string{"old"}}),
	}
	d.Present(snapshot)
	if d.projects.GetItemCount() != 2 || d.projectID != "hosted" || d.runID != "prior-setup" {
		t.Fatalf("migration lost project or run selection: rows=%d project=%s run=%s", d.projects.GetItemCount(), d.projectID, d.runID)
	}
	if len(d.runRows) != 3 || d.runRows[0].id != "current-run" || d.runRows[2].id != "prior-session" {
		t.Fatalf("history from both rows was not visible: %+v", d.runRows)
	}
}
