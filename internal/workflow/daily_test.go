package workflow

import (
	"context"
	"testing"
	"time"
)

func TestDisabledIdeationDoesNotCollectSources(t *testing.T) {
	e, _, _ := testEngine(t)
	ctx := context.Background()
	_ = e.Store.Put(ctx, "settings", "global", map[string]any{"global_slots": 4, "project_slots": 2, "reconciliation_seconds": 300, "ideation": map[string]any{"enabled": false, "time": "09:30", "timezone": "America/Denver"}}, 0)
	e.Daily(ctx)
	sources, _ := e.Store.List(ctx, "source_cache")
	if len(sources) != 0 {
		t.Fatal("automatic ideation ran while disabled")
	}
}

func TestConfiguredIdeationDueDateAcrossTimezoneAndDST(t *testing.T) {
	schedule := IdeationSchedule{Enabled: true, Time: "09:30", Timezone: "America/Denver"}
	for _, tc := range []struct{ now, day string }{
		{"2026-10-14T15:29:00Z", "2026-10-13"},
		{"2026-10-14T15:30:00Z", "2026-10-14"},
		{"2026-11-01T16:29:00Z", "2026-10-31"},
		{"2026-11-01T16:30:00Z", "2026-11-01"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		got, err := schedule.DueDay(now)
		if err != nil || got != tc.day {
			t.Fatalf("%s: day=%s error=%v", tc.now, got, err)
		}
	}
	// A skipped spring-forward time is due at the next available wall-clock minute.
	schedule.Time = "02:30"
	now, _ := time.Parse(time.RFC3339, "2026-03-08T09:00:00Z")
	if got, err := schedule.DueDay(now); err != nil || got != "2026-03-08" {
		t.Fatalf("DST gap: %s %v", got, err)
	}
}
