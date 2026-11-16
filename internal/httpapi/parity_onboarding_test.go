package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
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
