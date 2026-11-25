package dashboard

import (
	"strings"
	"testing"
	"time"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

const (
	providerFixtureLastActivity = "2026-01-01T12:00:01Z"
)

func TestAttemptInspectionSeparatesWorkerLeaseFromACPActivity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-2 * time.Minute).Format(time.RFC3339)
	recent := now.Add(-2 * time.Second).Format(time.RFC3339)
	old := now.Add(-time.Minute).Format(time.RFC3339)
	future := now.Add(time.Minute).Format(time.RFC3339)
	project := model.Project{ID: "sample", Name: "Local fixture"}
	d := New()
	d.Present(control.Snapshot{
		"projects":        {record(project.ID, project)},
		"execution_plans": {record("plan", model.ExecutionPlan{ID: "plan", ProjectID: project.ID, Stage: "planning"})},
		"sessions": {
			record("quiet", model.Attempt{AttemptID: "quiet", Task: "plan", ProjectID: project.ID, Role: "plan", Status: "running",
				StartedAt: start, ProviderProgressAt: start, WorkerHeartbeatAt: recent, LeaseExpiresAt: future, DeadlineAt: future}),
			record("disconnected", model.Attempt{AttemptID: "disconnected", Task: "plan", ProjectID: project.ID, Role: "code", Status: "running",
				StartedAt: start, ProviderProgressAt: recent, WorkerHeartbeatAt: old, LeaseExpiresAt: old}),
			record("terminated", model.Attempt{AttemptID: "terminated", Task: "plan", ProjectID: project.ID, Role: "plan", Status: "failed", Finished: true,
				StartedAt: start, ProviderProgressAt: start, FinishedAt: old, Error: "provider transport: provider exited before response: exit status 137"}),
		},
	})
	view := d.detail.GetText(false)
	for _, want := range []string{
		"heartbeat: lease valid", "heartbeat: expired",
		"ACP activity: no update since launch at " + start,
		"ACP activity: last update at " + recent,
		"provider transport: provider exited before response: exit status 137",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("missing independent evidence %q:\n%s", want, view)
		}
	}
}

func TestRunInspectionShowsACPActivityEvenWithoutAPlan(t *testing.T) {
	start := providerFixtureLastActivity
	last := "2026-01-01T12:00:03Z"
	d := New()
	project := model.Project{ID: "project", Name: "Local fixture"}
	attempt := model.Attempt{AttemptID: "ideation", ProjectID: project.ID, Role: "ideate", Status: "failed", Finished: true,
		StartedAt: start, ProviderProgressAt: last, FinishedAt: "2026-01-01T12:00:05Z", Error: "context deadline exceeded"}
	d.Present(control.Snapshot{"projects": {record(project.ID, project)}, "sessions": {record(attempt.AttemptID, attempt)}})
	d.selectRun(0, "", "", 0)
	view := d.detail.GetText(false)
	if !strings.Contains(view, "ACP activity: last update at "+last) || !strings.Contains(view, "Error: context deadline exceeded") {
		t.Fatalf("run did not preserve separate activity and deadline failure:\n%s", view)
	}
}

func TestProviderActivityDoesNotInventUpdateOrLiveness(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 10, 0, time.UTC)
	for _, test := range []struct{ name, started, progress, finished, want string }{
		{"missing", "", "", "", "unknown (not recorded)"},
		{"invalid", "", "bad value", "", "unknown (invalid update timestamp)"},
		{"future", "", "2026-01-01T12:00:11Z", "", "unknown (invalid update timestamp)"},
		{"legacy", "", providerFixtureLastActivity, "", "recorded at 2026-01-01T12:00:01Z (launch time not recorded)"},
		{"early", providerFixtureLastActivity, "2026-01-01T11:59:59Z", "", "unknown (update cannot be reconciled with launch)"},
		{"late", providerFixtureLastActivity, "2026-01-01T12:00:09Z", "2026-01-01T12:00:08Z", "unknown (update cannot be reconciled with finish)"},
		{"quiet", providerFixtureLastActivity, providerFixtureLastActivity, "", "no update since launch at 2026-01-01T12:00:01Z (9s ago)"},
		{"active", providerFixtureLastActivity, "2026-01-01T12:00:04Z", "", "last update at 2026-01-01T12:00:04Z (6s ago)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempt := model.Attempt{Status: "running", StartedAt: test.started, ProviderProgressAt: test.progress, FinishedAt: test.finished}
			if got := providerActivity(attempt, now); got != test.want {
				t.Fatalf("activity = %q, want %q", got, test.want)
			}
		})
	}
}
