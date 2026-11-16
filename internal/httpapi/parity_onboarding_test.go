package httpapi

import (
	"context"
	"encoding/json"
	"testing"
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
	if err != nil || len(operations) != 1 {
		t.Fatalf("initial operation missing: %d %v", len(operations), err)
	}
	var id string
	if err := s.Store.DB.QueryRow("SELECT run_id FROM ideation_runs").Scan(&id); err != nil || id != operations[0].ID {
		t.Fatalf("run identity diverged: %s %v", id, err)
	}
}
