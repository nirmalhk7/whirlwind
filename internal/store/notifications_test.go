package store

import (
	"context"
	"testing"
	"whirlwind/internal/model"
)

func TestRepeatedNotificationCoalescesAndResolvedAlertDoesNotReopen(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for range 2 {
		if err := s.Notify(ctx, "task", "failure", "same error"); err != nil {
			t.Fatal(err)
		}
	}
	records, _ := s.List(ctx, "inbox")
	if len(records) != 1 {
		t.Fatalf("duplicate alerts: %d", len(records))
	}
	n, _ := Decode[model.Notification](records[0])
	n.Resolved = true
	if err := s.Put(ctx, "inbox", records[0].ID, n, records[0].Version); err != nil {
		t.Fatal(err)
	}
	if err := s.Notify(ctx, "task", "failure", "same error"); err != nil {
		t.Fatal(err)
	}
	records, _ = s.List(ctx, "inbox")
	n, _ = Decode[model.Notification](records[0])
	if !n.Resolved {
		t.Fatal("unchanged dismissed alert reopened")
	}
	if err := s.Notify(ctx, "task", "failure", "different error"); err != nil {
		t.Fatal(err)
	}
	records, _ = s.List(ctx, "inbox")
	if len(records) != 2 {
		t.Fatal("materially changed failure was suppressed")
	}
}

func TestTaskTransitionNotificationCommitsWithTask(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SettleUsage(ctx, model.UsageEntry{TaskID: "task", AttemptID: "unknown"}); err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "task", Stage: "coding", Handoff: model.Handoff{NextAction: "implement accepted plan"}}
	if err := s.Put(ctx, "tasks", task.ID, task, 0); err != nil {
		t.Fatal(err)
	}
	task.Stage = "review"
	task.Handoff.NextAction = "review tested diff"
	if err := s.Put(ctx, "tasks", task.ID, task, 1); err != nil {
		t.Fatal(err)
	}
	records, _ := s.List(ctx, "inbox")
	if len(records) != 2 {
		t.Fatalf("missing durable milestones: %d", len(records))
	}
	alert, _ := Decode[model.Notification](records[0])
	if alert.Usage == nil || alert.Usage.Attempts != 1 || alert.Usage.UnknownTokenAttempts != 1 {
		t.Fatal("milestone hides unknown consumption")
	}
	if err := s.Put(ctx, "tasks", task.ID, task, 1); err == nil {
		t.Fatal("stale task write accepted")
	}
	after, _ := s.List(ctx, "inbox")
	if len(after) != len(records) {
		t.Fatal("failed transaction emitted notification")
	}
	if err := s.Put(ctx, "tasks", task.ID, task, 2); err != nil {
		t.Fatal(err)
	}
	after, _ = s.List(ctx, "inbox")
	if len(after) != len(records) {
		t.Fatal("unchanged task emitted another milestone")
	}
}
