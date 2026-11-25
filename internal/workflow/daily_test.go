package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	dailyFixtureFirstLocalSlot  = "2026-11-23T16:00:00Z"
	dailyFixtureSecondLocalSlot = "2026-11-23T17:00:00Z"
)

func TestDisabledIdeationDoesNotCollectSources(t *testing.T) {
	e, _, _ := testEngine(t)
	ctx := context.Background()
	record, err := e.Store.Get(ctx, "settings", "global")
	if err != nil {
		t.Fatal(err)
	}
	settings := e.settings(ctx)
	settings.Ideation.Cron = ""
	if err := e.Store.Put(ctx, "settings", "global", settings, record.Version); err != nil {
		t.Fatal(err)
	}
	e.Daily(ctx)
	sources, _ := e.Store.List(ctx, "source_cache")
	if len(sources) != 0 {
		t.Fatal("automatic ideation ran while disabled")
	}
}

func TestConfiguredIdeationDueDateAcrossTimezoneAndDST(t *testing.T) {
	schedule := IdeationSchedule{Cron: "CRON_TZ=America/Denver 30 9 * * *"}
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
	// The nonexistent local minute does not match the crontab expression.
	schedule.Cron = "CRON_TZ=America/Denver 30 2 * * *"
	now, _ := time.Parse(time.RFC3339, "2026-03-08T09:00:00Z")
	if got, err := schedule.DueDay(now); err != nil || got != "2026-03-07" {
		t.Fatalf("DST gap: %s %v", got, err)
	}
}

func TestCrontabIdeationSelectsLatestMatchingMinute(t *testing.T) {
	var schedule IdeationSchedule
	if err := json.Unmarshal([]byte(`{"cron":"CRON_TZ=America/Denver 0 9,10 * * MON-FRI"}`), &schedule); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ now, want string }{
		{"2026-11-23T15:59:59Z", "2026-11-20T17:00:00Z"},
		{dailyFixtureFirstLocalSlot, dailyFixtureFirstLocalSlot},
		{"2026-11-23T16:59:59Z", dailyFixtureFirstLocalSlot},
		{dailyFixtureSecondLocalSlot, dailyFixtureSecondLocalSlot},
		{"2026-11-28T18:00:00Z", "2026-11-27T17:00:00Z"},
	} {
		now, err := time.Parse(time.RFC3339, test.now)
		if err != nil {
			t.Fatal(err)
		}
		start, err := schedule.latestOccurrence(now)
		if err != nil || start.UTC().Format(time.RFC3339) != test.want {
			t.Fatalf("at %s got %s, %v; want %s", test.now, start, err, test.want)
		}
	}
}

func TestCrontabRepeatedDSTMinuteKeepsDistinctOccurrences(t *testing.T) {
	schedule := IdeationSchedule{Cron: "CRON_TZ=America/Denver 30 1 * * *"}
	for _, value := range []string{"2026-11-01T07:30:00Z", "2026-11-01T08:30:00Z"} {
		now, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := schedule.latestOccurrence(now)
		if err != nil || !got.Equal(now) {
			t.Fatalf("at %s got %s (%v)", now, got, err)
		}
	}
}

func TestCrontabDispatchDeduplicatesByOccurrence(t *testing.T) {
	e, task, _ := testEngine(t)
	e.GitHub = disconnectedGitHub{}
	ctx := context.Background()
	schedule := IdeationSchedule{Cron: "CRON_TZ=America/Denver 0 9,10 * * MON-FRI"}
	for _, when := range []string{dailyFixtureFirstLocalSlot, "2026-11-23T16:00:02Z", dailyFixtureSecondLocalSlot} {
		now, err := time.Parse(time.RFC3339, when)
		if err != nil {
			t.Fatal(err)
		}
		e.runIdeationSchedule(ctx, schedule, now)
	}
	for _, when := range []string{dailyFixtureFirstLocalSlot, dailyFixtureSecondLocalSlot} {
		record, err := e.Store.Get(ctx, "schedules", task.ProjectID+":"+when)
		if err != nil || record.Version != 1 {
			t.Fatalf("%s: expected a single recorded outcome, got version %d (%v)", when, record.Version, err)
		}
		if !strings.Contains(string(record.Data), `"day":"2026-11-23"`) {
			t.Fatalf("display date lost: %s", record.Data)
		}
	}
}
