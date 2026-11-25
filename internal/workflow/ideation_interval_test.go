package workflow

import (
	"encoding/json"
	"testing"
	"time"
)

func TestElapsedIdeationKeepsDistinctStoredSlots(t *testing.T) {
	schedule := DefaultSettings().Ideation
	requireWorkflowSuccess(t, json.Unmarshal([]byte(`{"interval_minutes":60,"timezone":"America/Denver"}`), &schedule))
	requireWorkflowSuccess(t, schedule.Validate())
	firstTime := time.Date(2026, 11, 1, 7, 15, 0, 0, time.UTC)
	first, err := schedule.latestOccurrence(firstTime)
	requireWorkflowSuccess(t, err)
	same, err := schedule.latestOccurrence(firstTime.Add(25 * time.Minute))
	requireWorkflowSuccess(t, err)
	next, err := schedule.latestOccurrence(firstTime.Add(time.Hour))
	requireWorkflowSuccess(t, err)
	if !first.Equal(same) || next.Sub(first) != time.Hour || first.Hour() != next.Hour() {
		t.Fatalf("unexpected repeated-hour slots: %s, %s, %s", first, same, next)
	}
	e, task, _ := testEngine(t)
	for _, slot := range []time.Time{first, next} {
		key := task.ProjectID + ":" + slot.UTC().Format(time.RFC3339)
		if err := e.Store.Put(t.Context(), "schedules", key, map[string]string{"stage": "complete"}, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Store.Get(t.Context(), "schedules", key); err != nil {
			t.Fatal(err)
		}
	}
}

func TestElapsedIdeationRejectsAmbiguousSettings(t *testing.T) {
	for _, raw := range []string{`{"interval_minutes":10081}`, `{"interval_minutes":9,"cron":"* * * * *"}`, `{"interval_minutes":7,"interval_timezone":"Local"}`} {
		var schedule IdeationSchedule
		err := json.Unmarshal([]byte(raw), &schedule)
		if err == nil && schedule.Validate() == nil {
			t.Errorf("accepted invalid schedule %s", raw)
		}
	}
}
