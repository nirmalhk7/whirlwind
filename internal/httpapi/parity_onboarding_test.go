package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	onboardingFixtureCompletedSetup = "completed-setup"
	onboardingFixtureFailedSetup    = "failed-setup"
)

func TestParityOnboardingQueuesExactlyOneDurableIdeation(t *testing.T) {
	s := setup(t)
	repo := t.TempDir()
	mustCommand(t, repo, "git", "init", "--quiet")
	payload, _ := json.Marshal(map[string]string{"name": "Fresh project", "path": repo})
	command := LocalCommand{Operation: "project.register", IdempotencyKey: "onboarding", Payload: payload}
	first, err := s.ExecuteLocal(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ExecuteLocal(context.Background(), command)
	if err != nil || string(first) != string(second) {
		t.Fatalf("registration receipt changed: %v", err)
	}
	var count int
	if err := s.Store.DB.QueryRow("SELECT count(*) FROM ideation_runs WHERE status='queued'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("initial queued run count=%d: %v", count, err)
	}
	operations, err := s.Store.List(context.Background(), "operations")
	if err != nil {
		t.Fatal(err)
	}
	var ideationID string
	for _, record := range operations {
		op, decodeErr := store.Decode[model.Operation](record)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if op.Kind == "ideation" {
			if ideationID != "" {
				t.Fatal("duplicate initial ideation operation")
			}
			ideationID = record.ID
		}
	}
	var id string
	if err := s.Store.DB.QueryRow("SELECT run_id FROM ideation_runs").Scan(&id); err != nil || id != ideationID {
		t.Fatalf("run identity diverged: %s %v", id, err)
	}
}

func TestOnboardingQueuesOneSetupBeforeProjectCanBeReady(t *testing.T) {
	s := setup(t)
	repo := t.TempDir()
	mustCommand(t, repo, "git", "init", "--quiet")
	payload, _ := json.Marshal(map[string]string{"name": "Pending setup", "path": repo})
	command := LocalCommand{Operation: "project.register", IdempotencyKey: "setup-once", Payload: payload}
	for range 2 {
		if _, err := s.ExecuteLocal(context.Background(), command); err != nil {
			t.Fatal(err)
		}
	}
	projects, err := s.Store.List(context.Background(), "projects")
	if err != nil || len(projects) != 1 {
		t.Fatalf("registered project missing: %v", err)
	}
	project, err := store.Decode[model.Project](projects[0])
	if err != nil || project.Status != "draft" {
		t.Fatalf("unfinished setup must remain draft: %+v %v", project, err)
	}
	operations, err := s.Store.List(context.Background(), "operations")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, record := range operations {
		op, err := store.Decode[model.Operation](record)
		if err != nil {
			t.Fatal(err)
		}
		if op.Kind == "project_setup" && op.Subject == project.ID && op.Stage == "queued" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one durable setup operation, got %d", count)
	}
}

func TestProjectSetupRetryCreatesNewDurableOperation(t *testing.T) {
	server := setup(t)
	ctx := context.Background()
	project := model.Project{ID: "retry-project", Name: "Retry", Path: t.TempDir(), Status: "error"}
	if err := server.Store.Put(ctx, "projects", project.ID, project, 0); err != nil {
		t.Fatal(err)
	}
	failed := model.Operation{Kind: "project_setup", Subject: project.ID, Stage: "failed", Error: "required scanner was unavailable", Input: map[string]any{"onboarding": true, "checkpoint": "preserve"}}
	if err := server.Store.Put(ctx, "operations", onboardingFixtureFailedSetup, failed, 0); err != nil {
		t.Fatal(err)
	}
	if err := server.Store.Put(ctx, "operations", "initial-ideation", model.Operation{Kind: "ideation", Subject: project.ID, Stage: "queued"}, 0); err != nil {
		t.Fatal(err)
	}
	command := LocalCommand{Operation: "project.setup.retry", ProjectID: project.ID, ExpectedVersion: 1, IdempotencyKey: "retry-once"}
	first, err := server.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.ExecuteLocal(ctx, command)
	if err != nil || string(first) != string(second) {
		t.Fatalf("retry receipt changed: %s / %s (%v)", first, second, err)
	}
	var receipt model.Record
	if err := json.Unmarshal(first, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.ID == onboardingFixtureFailedSetup || receipt.Version != 1 {
		t.Fatalf("retry reused old setup journal identity: %+v", receipt)
	}
	newOperation, err := store.Decode[model.Operation](receipt)
	if err != nil || newOperation.Kind != "project_setup" || newOperation.Subject != project.ID || newOperation.Stage != "queued" {
		t.Fatalf("retry did not queue new setup operation: %+v (%v)", newOperation, err)
	}
	var retryInput struct {
		Onboarding bool `json:"onboarding"`
	}
	encoded, _ := json.Marshal(newOperation.Input)
	if err := json.Unmarshal(encoded, &retryInput); err != nil || !retryInput.Onboarding {
		t.Fatalf("retry dropped automatic onboarding mode: %+v (%v)", newOperation.Input, err)
	}
	savedProject, err := server.Store.Get(ctx, "projects", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project, err = store.Decode[model.Project](savedProject)
	if err != nil || project.Status != "draft" {
		t.Fatalf("retry exposed project as ready or error instead of pending: %+v (%v)", project, err)
	}
	oldRecord, err := server.Store.Get(ctx, "operations", onboardingFixtureFailedSetup)
	if err != nil {
		t.Fatal(err)
	}
	oldOperation, err := store.Decode[model.Operation](oldRecord)
	if err != nil || oldOperation.Stage != "failed" || oldOperation.Error == "" {
		t.Fatalf("retry erased prior failure evidence: %+v (%v)", oldOperation, err)
	}
	operations, err := server.Store.List(ctx, "operations")
	if err != nil {
		t.Fatal(err)
	}
	setupCount, ideationCount := 0, 0
	for _, record := range operations {
		operation, err := store.Decode[model.Operation](record)
		if err != nil {
			t.Fatal(err)
		}
		if operation.Subject != project.ID {
			continue
		}
		if operation.Kind == "project_setup" {
			setupCount++
		}
		if operation.Kind == "ideation" {
			ideationCount++
		}
	}
	if setupCount != 2 || ideationCount != 1 {
		t.Fatalf("retry duplicated initial onboarding work: setup=%d ideation=%d", setupCount, ideationCount)
	}
	stale := command
	stale.ExpectedVersion = 1
	stale.IdempotencyKey = "stale-retry"
	if _, err := server.ExecuteLocal(ctx, stale); err == nil {
		t.Fatal("stale project version started another setup retry")
	}
}

func TestProjectSetupRetryAllowsExplicitRecheckOfCompletedSetup(t *testing.T) {
	server := setup(t)
	ctx := context.Background()
	project := model.Project{ID: "recheck-project", Name: "Recheck", Path: t.TempDir(), Status: "approved", QualityIdentity: &model.QualityIdentity{Version: 1}}
	if err := server.Store.Put(ctx, "projects", project.ID, project, 0); err != nil {
		t.Fatal(err)
	}
	completed := model.Operation{Kind: "project_setup", Subject: project.ID, Stage: "complete", Input: map[string]any{"onboarding": true}}
	if err := server.Store.Put(ctx, "operations", onboardingFixtureCompletedSetup, completed, 0); err != nil {
		t.Fatal(err)
	}
	if err := server.Store.Put(ctx, "operations", "initial-ideation", model.Operation{Kind: "ideation", Subject: project.ID, Stage: "queued"}, 0); err != nil {
		t.Fatal(err)
	}
	command := LocalCommand{Operation: "project.setup.retry", ProjectID: project.ID, ExpectedVersion: 1, IdempotencyKey: "recheck-once"}
	first, err := server.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatalf("completed setup cannot be explicitly rechecked: %v", err)
	}
	second, err := server.ExecuteLocal(ctx, command)
	if err != nil || string(first) != string(second) {
		t.Fatalf("recheck receipt changed: %s / %s (%v)", first, second, err)
	}
	var receipt model.Record
	if err := json.Unmarshal(first, &receipt); err != nil {
		t.Fatal(err)
	}
	recheck, err := store.Decode[model.Operation](receipt)
	if err != nil || receipt.ID == onboardingFixtureCompletedSetup || recheck.Stage != "queued" {
		t.Fatalf("recheck did not create a fresh setup operation: %+v (%v)", recheck, err)
	}
	var input struct {
		Onboarding bool `json:"onboarding"`
	}
	encoded, _ := json.Marshal(recheck.Input)
	if err := json.Unmarshal(encoded, &input); err != nil || !input.Onboarding {
		t.Fatalf("recheck dropped automatic qualification mode: %+v (%v)", recheck.Input, err)
	}
	saved, err := server.Store.Get(ctx, "projects", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project, err = store.Decode[model.Project](saved)
	if err != nil || project.Status != "draft" || project.QualityIdentity != nil {
		t.Fatalf("recheck left stale qualification ready: %+v (%v)", project, err)
	}
	if _, err := server.ExecuteLocal(ctx, LocalCommand{Operation: command.Operation, ProjectID: project.ID, ExpectedVersion: 1, IdempotencyKey: "duplicate-recheck"}); err == nil {
		t.Fatal("recheck accepted a second request after project version advanced")
	}
	old, err := server.Store.Get(ctx, "operations", onboardingFixtureCompletedSetup)
	if err != nil {
		t.Fatal(err)
	}
	oldOperation, err := store.Decode[model.Operation](old)
	if err != nil || oldOperation.Stage != "complete" {
		t.Fatalf("recheck altered completed evidence: %+v (%v)", oldOperation, err)
	}
}
