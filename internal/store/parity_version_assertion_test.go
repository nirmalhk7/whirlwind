package store

import (
	"context"
	"errors"
	"testing"
)

type stagedVersionAsserter interface {
	AssertVersion(context.Context, string, string, int) error
}

func TestParityContainerVersionAssertionRollsBackQueuedMutation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.Put(ctx, "projects", "p", map[string]any{"id": "p"}, 0); err != nil {
		t.Fatal(err)
	}
	staged, batch := Stage(ctx)
	guard, ok := any(s).(stagedVersionAsserter)
	if !ok {
		t.Fatal("transactional container-version guard unavailable")
	}
	if err := guard.AssertVersion(staged, "projects", "p", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "projects", "p", map[string]any{"id": "p", "paused": true}, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(staged, "operations", "queued", map[string]any{"kind": "other", "stage": "queued"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := batch.Commit(staged, s, &Receipt{Key: "mutation", Fingerprint: "intent", Status: 200, Body: []byte(`{}`)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale container guard committed: %v", err)
	}
	if _, err := s.Get(ctx, "operations", "queued"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale queue mutation escaped rollback: %v", err)
	}
	if status, _, err := s.Idempotent(ctx, "mutation", "intent"); err != nil || status != 0 {
		t.Fatalf("stale decision acquired receipt: %d %v", status, err)
	}
}
