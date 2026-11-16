package store

import (
	"context"
	"testing"
	"time"
	"whirlwind/internal/model"
)

func TestParityFinalizationCannotPublishAfterAttemptDeadline(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	plan := model.ExecutionPlan{ID: "plan", ProjectID: "p", PlanVersion: 1, Stage: "submitting"}
	if err := s.Put(ctx, "execution_plans", "plan", plan, 0); err != nil {
		t.Fatal(err)
	}
	attempt := model.Attempt{AttemptID: "attempt", ExecutionID: "attempt", ExecutionGeneration: 1, Task: "plan", ProjectID: "p", PlanVersion: 1, Owner: "supervisor", Role: "finalizer", Purpose: "local_finalization", Status: "running", LeaseExpiresAt: time.Now().Add(20 * time.Second).UTC().Format(time.RFC3339Nano), DeadlineAt: time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)}
	if err := s.Put(ctx, "sessions", "attempt", attempt, 0); err != nil {
		t.Fatal(err)
	}
	planRecord, _ := s.Get(ctx, "execution_plans", "plan")
	attemptRecord, _ := s.Get(ctx, "sessions", "attempt")
	published := false
	err := s.CommitLocalDelivery(ctx, planRecord, attemptRecord, LocalDeliveryOwner{Owner: "supervisor", Generation: 1}, "plan:1", model.LocalFinalization{PlanID: "plan", ProjectID: "p", PlanVersion: 1}, func() error { published = true; return nil })
	if err == nil || published {
		t.Fatalf("expired ACP attempt published a delivery: published=%v err=%v", published, err)
	}
	if _, err := s.Get(ctx, "finalizations", "plan:1"); err == nil {
		t.Fatal("expired attempt persisted delivery receipt")
	}
}
