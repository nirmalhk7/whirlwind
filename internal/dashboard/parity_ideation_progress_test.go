package dashboard

import (
	"strings"
	"testing"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func TestParityRunsDisplayDurableIdeationProgressWithoutActiveAgent(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repository"})},
		"operations": {record("source", map[string]any{"kind": "ideation", "subject": "p", "stage": "partial", "ideation_directions": []map[string]any{
			{"direction": "expand", "candidate_count": 2, "status": "completed", "idea_id": "accepted-idea"},
			{"direction": "harden", "candidate_count": 5, "status": "error", "error_reason": "No qualifying candidate"},
		}})},
	})
	if len(d.runRows) != 1 {
		t.Fatalf("durable ideation run disappears without an ACP process: %d", len(d.runRows))
	}
	d.selectRun(0, "", "", 0)
	for _, want := range []string{"source", "partial", "expand: 2/5", "harden: 5/5", "accepted-idea", "No qualifying candidate", "Current uniqueness policy: Deferred"} {
		if !strings.Contains(d.detail.GetText(false), want) {
			t.Fatalf("run progress missing %q: %s", want, d.detail.GetText(false))
		}
	}
	d.showRunLogs(0)
	if d.modal == nil {
		t.Fatal("source run tried to tail a nonexistent agent log instead of inspecting durable progress")
	}
}
