package httpapi

import (
	"encoding/json"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestLocalRegistrationCanOptOutOfInitialIdeation(t *testing.T) {
	s := setup(t)
	ctx := t.Context()
	if err := s.Store.Put(ctx, "settings", "global", map[string]any{"ideation": map[string]any{"on_registration": false, "cron": ""}}, 0); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	mustCommand(t, repo, "git", "init", "--quiet")
	payload, _ := json.Marshal(map[string]string{"name": "No model call", "path": repo})
	command := LocalCommand{Operation: "project.register", IdempotencyKey: "opt-out", Payload: payload}
	first, err := s.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ExecuteLocal(ctx, command)
	if err != nil || string(second) != string(first) {
		t.Fatalf("registration receipt changed: %s / %s, %v", first, second, err)
	}
	operations, err := s.Store.List(ctx, "operations")
	if err != nil || len(operations) != 1 {
		t.Fatalf("opt-out should register once and enqueue only setup, got %d operations: %v", len(operations), err)
	}
	op, err := store.Decode[model.Operation](operations[0])
	if err != nil || op.Kind != "project_setup" || op.Stage != "queued" {
		t.Fatalf("opting out of ideation should not suppress project setup: %+v %v", op, err)
	}
}
