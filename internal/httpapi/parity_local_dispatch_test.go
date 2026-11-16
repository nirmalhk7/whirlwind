package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestParityManualIdeationReturnsDurablyQueuedRunWithoutLaunchingACP(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "projects", "p", model.Project{ID: "p", Name: "Project", Path: t.TempDir()}, 0); err != nil {
		t.Fatal(err)
	}
	project, err := s.Store.Get(ctx, "projects", "p")
	if err != nil {
		t.Fatal(err)
	}
	command := LocalCommand{Operation: "ideation.start", ProjectID: "p", ExpectedVersion: project.Version, IdempotencyKey: "manual", Payload: json.RawMessage(`{"fresh":true}`)}
	raw, err := s.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]string
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	operation, err := s.Store.Get(ctx, "operations", response["operation_id"])
	if err != nil {
		t.Fatal(err)
	}
	run, _ := store.Decode[model.Operation](operation)
	if run.Kind != "ideation" || run.Stage != "queued" {
		t.Fatalf("control request bypassed durable supervisor queue: %+v", run)
	}
	replay, err := s.ExecuteLocal(ctx, command)
	if err != nil || string(replay) != string(raw) {
		t.Fatalf("manual run idempotency lost: %v", err)
	}
	records, err := s.Store.List(ctx, "operations")
	if err != nil || len(records) != 1 {
		t.Fatalf("manual replay queued another run: %d %v", len(records), err)
	}
}

func TestParityPlanActivityRejectsWrongProjectScope(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	plan := model.ExecutionPlan{ID: "plan", ProjectID: "p", Stage: "coding", PlanVersion: 1, Selection: &model.SelectionAuthorization{CandidateID: "idea", SelectedAt: "now"}}
	if err := s.Store.Put(ctx, "execution_plans", plan.ID, plan, 0); err != nil {
		t.Fatal(err)
	}
	command := LocalCommand{Operation: "plan.pause", ProjectID: "other", PlanID: plan.ID, ExpectedVersion: 1, IdempotencyKey: "wrong-scope"}
	if _, err := s.ExecuteLocal(ctx, command); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("plan mutation expanded project authorization: %v", err)
	}
	record, err := s.Store.Get(ctx, "execution_plans", plan.ID)
	if err != nil || record.Version != 1 {
		t.Fatalf("wrong-scope activity changed plan: %+v %v", record, err)
	}
	command.ProjectID, command.IdempotencyKey = "p", "pause"
	first, err := s.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.ExecuteLocal(ctx, command)
	if err != nil || string(first) != string(replay) {
		t.Fatalf("pause receipt changed: %v", err)
	}
	command.Operation, command.ExpectedVersion, command.IdempotencyKey = "plan.resume", 2, "resume"
	if _, err := s.ExecuteLocal(ctx, command); err != nil {
		t.Fatal(err)
	}
	record, err = s.Store.Get(ctx, "execution_plans", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed, _ := store.Decode[model.ExecutionPlan](record)
	if resumed.Stage != "coding" || resumed.RecoveryGeneration != 1 {
		t.Fatalf("explicit resume lost durable stage/recovery epoch: %+v", resumed)
	}
}
