package dashboard

import (
	"path/filepath"
	"testing"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func TestParityExecutionPlanSnapshotIsNavigable(t *testing.T) {
	d := New(t.TempDir())
	d.Present(control.Snapshot{
		"projects":        {record("p", model.Project{ID: "p", Name: "Project"})},
		"execution_plans": {record("approved", model.ExecutionPlan{ID: "approved", ProjectID: "p", Stage: "coding", Issue: model.Issue{Title: "Approved objective"}})},
	})
	if len(d.planRows) != 1 || d.planRows[0].ID != "approved" {
		t.Fatal("approved execution plan is missing from project navigation")
	}
}

func TestParityDashboardUsesFreshStateByDefault(t *testing.T) {
	t.Setenv("WHIRLWIND_DATA_DIR", "")
	if d := New(); filepath.Base(d.dataDirectory) != "parity-v1" {
		t.Fatalf("dashboard would open legacy state: %s", d.dataDirectory)
	}
}
