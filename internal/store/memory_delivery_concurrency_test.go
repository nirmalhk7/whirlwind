package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"whirlwind/internal/model"
)

const memoryDeliveryOperation = "same-operation"

func TestConcurrentMemoryDeliveriesDeduplicateLessonVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	stores := []*Store{openMemoryBudgetStore(t, path), openMemoryBudgetStore(t, path)}
	ctx := context.Background()
	for _, database := range stores {
		if _, err := database.ReserveMemorySearch(ctx, memoryDeliveryOperation); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	refs := []model.LessonRef{{ID: "same-lesson", Version: 1}}
	for _, database := range stores {
		go func(database *Store) {
			<-start
			results <- database.RecordMemoryDelivery(ctx, memoryDeliveryOperation, refs, 100)
		}(database)
	}
	close(start)
	successes := 0
	for range stores {
		err := <-results
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("duplicate deliveries accepted: got %d successes", successes)
	}
	budget, err := stores[0].MemoryAllowance(ctx, memoryDeliveryOperation)
	if err != nil || budget.Bytes != 100 || len(budget.Seen) != 1 {
		t.Fatalf("duplicate delivery altered durable allowance: %+v %v", budget, err)
	}
}
