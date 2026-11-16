package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"whirlwind/internal/model"
)

func TestParityIdeationHistoryDebitsAreAtomicAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(path)
	requireStoreSuccess(t, err)
	t.Cleanup(func() { _ = first.Close() })
	second, err := Open(path)
	requireStoreSuccess(t, err)
	t.Cleanup(func() { _ = second.Close() })
	ctx := context.Background()
	requireStoreSuccess(t, first.Put(ctx, "operations", "run", model.Operation{Kind: "ideation", Subject: "project", Stage: "running"}, 0))
	if _, err := first.InitializeIdeationHistoryBudget(ctx, "run", "project", 1000); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, db := range []*Store{first, second} {
		go func(s *Store) {
			<-start
			_, err := s.ConsumeIdeationHistory(ctx, "run", "project", 600)
			results <- err
		}(db)
	}
	close(start)
	successes, exhausted := 0, 0
	for n := 0; n < 2; n++ {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrHistoryBudget) {
			exhausted++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || exhausted != 1 {
		t.Fatalf("concurrent retrievals exceeded total allowance: success=%d exhausted=%d", successes, exhausted)
	}
	remaining, err := second.InitializeIdeationHistoryBudget(ctx, "run", "project", 1000)
	if err != nil || remaining != 400 {
		t.Fatalf("reinitialization reset consumed history: remaining=%d err=%v", remaining, err)
	}
	if _, err := second.InitializeIdeationHistoryBudget(ctx, "run", "project", 2000); !errors.Is(err, ErrConflict) {
		t.Fatalf("run allowance silently expanded: %v", err)
	}
	if _, err := second.ConsumeIdeationHistory(ctx, "run", "another-project", 1); err == nil {
		t.Fatal("history budget crossed project scope")
	}
}
