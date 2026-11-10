package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestLocalDirectionDeleteRemovesOnlySelectedProposalAndReplays(t *testing.T) {
	server := setup(t)
	ctx := context.Background()
	for _, id := range []string{"keep", "delete"} {
		if err := server.Store.Put(ctx, "directions", id, model.Direction{ID: id, ProjectID: "p", GroupID: "same-group", Title: id}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := server.Store.Put(ctx, "sessions", "run", model.Attempt{AttemptID: "run", ProjectID: "p", Role: "ideate"}, 0); err != nil {
		t.Fatal(err)
	}
	artifact, err := server.Store.Capture(ctx, "run", "output", "text/plain", []byte("retained logs"), 100, false)
	if err != nil {
		t.Fatal(err)
	}
	command := LocalCommand{Operation: "direction.delete", ResourceID: "delete", ProjectID: "p", ExpectedVersion: 1, IdempotencyKey: "delete-one", Payload: json.RawMessage(`{}`)}
	first, err := server.ExecuteLocal(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.ExecuteLocal(ctx, command)
	if err != nil || string(first) != string(second) {
		t.Fatalf("delete replay failed: %s %s %v", first, second, err)
	}
	if _, err := server.Store.Get(ctx, "directions", "delete"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("proposal still active: %v", err)
	}
	if _, err := server.Store.Get(ctx, "directions", "keep"); err != nil {
		t.Fatalf("peer proposal deleted: %v", err)
	}
	_, body, err := server.Store.Artifact(ctx, artifact.ID)
	if err != nil || string(body) != "retained logs" {
		t.Fatalf("run logs removed: %v", err)
	}
}

func TestLocalDirectionDeleteRejectsStaleVersion(t *testing.T) {
	server := setup(t)
	ctx := context.Background()
	if err := server.Store.Put(ctx, "directions", "idea", model.Direction{ID: "idea", ProjectID: "p"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ExecuteLocal(ctx, LocalCommand{Operation: "direction.delete", ResourceID: "idea", ExpectedVersion: 2, IdempotencyKey: "stale-delete"}); err == nil {
		t.Fatal("stale delete succeeded")
	}
	if _, err := server.Store.Get(ctx, "directions", "idea"); err != nil {
		t.Fatalf("stale delete removed current proposal: %v", err)
	}
}
