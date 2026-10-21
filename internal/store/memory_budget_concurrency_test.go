package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryBudgetConcurrentReservationsPersistAcrossStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	first := openMemoryBudgetStore(t, path)
	second := openMemoryBudgetStore(t, path)
	stores := []*Store{first, second}
	start := make(chan struct{})
	results := make(chan memoryReservationResult, 12)
	for i := range 12 {
		go func(s *Store) {
			<-start
			budget, err := s.ReserveMemorySearch(context.Background(), "operation")
			results <- memoryReservationResult{budget, err}
		}(stores[i%len(stores)])
	}
	close(start)
	assertMemoryReservationsBounded(t, results)
	assertMemoryBudgetPersisted(t, openMemoryBudgetStore(t, path))
}

type memoryReservationResult struct {
	budget MemoryBudget
	err    error
}

func openMemoryBudgetStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func assertMemoryReservationsBounded(t *testing.T, results <-chan memoryReservationResult) {
	t.Helper()
	successes := map[int]bool{}
	for range 12 {
		result := <-results
		if result.err != nil {
			if !strings.Contains(result.err.Error(), "allowance exhausted") {
				t.Errorf("reservation failed unexpectedly: %v", result.err)
			}
			continue
		}
		if result.budget.Calls < 1 || result.budget.Calls > 2 || successes[result.budget.Calls] {
			t.Errorf("duplicate or excessive reservation: %+v", result.budget)
		}
		successes[result.budget.Calls] = true
	}
	if len(successes) != 2 {
		t.Fatalf("successful slots = %v; want exactly 1 and 2", successes)
	}
}

func assertMemoryBudgetPersisted(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	record, err := s.Get(ctx, "memory_budgets", "operation")
	if err != nil {
		t.Fatal(err)
	}
	budget, err := Decode[MemoryBudget](record)
	if err != nil || budget.Calls != 2 {
		t.Fatalf("persisted budget: %+v %v", budget, err)
	}
	if _, err = s.ReserveMemorySearch(ctx, "operation"); err == nil {
		t.Fatal("reopened store reset exhausted allowance")
	}
	other, err := s.ReserveMemorySearch(ctx, "another-operation")
	if err != nil || other.Calls != 1 {
		t.Fatalf("operation budgets leaked: %+v %v", other, err)
	}
}
