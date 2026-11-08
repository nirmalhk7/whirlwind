package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRecoveryPreservesPendingHumanDecisionsAndInvalidatesAttempt(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, change := range []Change{
		{Kind: "sessions", ID: "attempt", Value: map[string]any{"finished": false, "status": "running", "owner": "old", "attempt_id": "attempt"}},
		{Kind: "questions", ID: "question", Value: map[string]any{"status": "pending", "attempt_id": "attempt", "questions": []any{map[string]any{"id": "q1", "text": "Which behavior?"}}}},
		{Kind: "permissions", ID: "permission", Value: map[string]any{"decision": "pending", "attempt_id": "attempt", "request": map[string]any{"tool": "write"}}},
	} {
		if err := s.Put(ctx, change.Kind, change.ID, change.Value, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Claim(ctx, "project", "old", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInterrupted(ctx, "new"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, id, field, want string }{
		{"sessions", "attempt", "status", "interrupted"},
		{"questions", "question", "status", "pending"},
		{"permissions", "permission", "decision", "pending"},
	} {
		record, err := s.Get(ctx, tc.kind, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err = json.Unmarshal(record.Data, &got); err != nil {
			t.Fatal(err)
		}
		if got[tc.field] != tc.want {
			t.Errorf("%s/%s %s = %v, want %q", tc.kind, tc.id, tc.field, got[tc.field], tc.want)
		}
		if tc.kind != "sessions" && got["attempt_id"] != "attempt" {
			t.Errorf("%s/%s lost owning attempt reference: %v", tc.kind, tc.id, got["attempt_id"])
		}
	}
	if _, err := s.Claim(ctx, "project", "replacement", time.Minute); err != nil {
		t.Fatalf("old owner lease was not revoked: %v", err)
	}
}

func TestSupervisorRecoveryPreservesOriginalAttemptDeadline(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	deadline := time.Now().Add(40 * time.Second).UTC().Format(time.RFC3339Nano)
	for _, change := range []Change{
		{Kind: "sessions", ID: "attempt", Value: map[string]any{"finished": false, "status": "running", "owner": "old", "attempt_id": "attempt", "started_at": time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), "deadline_at": deadline}},
		{Kind: "recovery_episodes", ID: "entry", Value: map[string]any{"episode_id": "episode", "entry_id": "entry", "restart_count": 1, "status": "running", "owner": "old"}},
	} {
		if err := s.Put(ctx, change.Kind, change.ID, change.Value, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecoverInterrupted(ctx, "new"); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.Get(ctx, "sessions", "attempt")
	if err != nil {
		t.Fatal(err)
	}
	var attemptState map[string]any
	if err = json.Unmarshal(attempt.Data, &attemptState); err != nil {
		t.Fatal(err)
	}
	if attemptState["deadline_at"] != deadline || attemptState["status"] != "interrupted" {
		t.Fatalf("recovery changed original attempt deadline or status: %s", attempt.Data)
	}
	episode, err := s.Get(ctx, "recovery_episodes", "entry")
	if err != nil {
		t.Fatal(err)
	}
	var episodeState map[string]any
	if err = json.Unmarshal(episode.Data, &episodeState); err != nil {
		t.Fatal(err)
	}
	if episodeState["status"] != "interrupted" || episodeState["next_retry_at"] == nil {
		t.Fatalf("recovery episode did not persist its restart/backoff state: %s", episode.Data)
	}
}
