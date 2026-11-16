package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestParityLostGenerationFenceRejectsPublication(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	lease, err := s.Claim(ctx, "generation:p", "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithLeaseFence(ctx, "generation:p", lease)
	if _, err := s.DB.Exec("UPDATE leases SET expires=0 WHERE id='generation:p'"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "audit", "stale", map[string]string{"action": "publish"}, 0); !errors.Is(err, ErrLeased) {
		t.Fatalf("stale writer published after losing lease: %v", err)
	}
	if _, err := s.Get(context.Background(), "audit", "stale"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale publication persisted: %v", err)
	}
}
