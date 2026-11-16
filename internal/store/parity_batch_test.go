package store

import (
	"context"
	"errors"
	"testing"
)

func TestParityAggregateWriteWaitsForCommandReceipt(t *testing.T) {
	s := openTest(t)
	ctx, b := Stage(context.Background())
	if err := s.Write(ctx, []Change{{Kind: "audit", ID: "decision", Value: map[string]string{"action": "approve"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(context.Background(), "audit", "decision"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uncommitted command leaked write: %v", err)
	}
	if err := b.Commit(ctx, s, &Receipt{Key: "command", Fingerprint: "same", Status: 200, Body: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(context.Background(), "audit", "decision"); err != nil {
		t.Fatal(err)
	}
	if status, _, err := s.Idempotent(context.Background(), "command", "same"); err != nil || status != 200 {
		t.Fatalf("receipt missing: %d %v", status, err)
	}
}
