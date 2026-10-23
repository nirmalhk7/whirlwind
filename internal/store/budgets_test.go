package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"whirlwind/internal/model"
)

// Removing transactional reservation would allow both contenders through the same task ceiling.
func TestBudgetAdmissionSerializesConcurrentAttempts(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	budget := model.BudgetPolicy{MaxAttempts: 1, MaxRuntimeSeconds: 60}
	var wg sync.WaitGroup
	wins := 0
	var mu sync.Mutex
	for _, id := range []string{"first", "second"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			err := s.ReserveBudget(ctx, model.BudgetReservation{AttemptID: id, TaskID: "task", Owner: "owner", Status: "active"}, budget)
			if err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			} else if !errors.Is(err, ErrBudgetExceeded) {
				t.Errorf("admission: %v", err)
			}
		}(id)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("admitted %d attempts against limit 1", wins)
	}
}

// Summing cumulative snapshots or accepting duplicate completions would inflate task consumption.
func TestUsageLedgerReconcilesSessionDeltasIdempotently(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n := int64(100)
	first := model.UsageEntry{AttemptID: "first", TaskID: "task", Provider: "codex", Model: "m", SessionID: "s", Usage: &model.Usage{Scope: "session", TotalTokens: &n}}
	if err := s.SettleUsage(ctx, first); err != nil {
		t.Fatal(err)
	}
	n2 := int64(140)
	second := model.UsageEntry{AttemptID: "second", TaskID: "task", Provider: "codex", Model: "m", SessionID: "s", Usage: &model.Usage{Scope: "session", TotalTokens: &n2}}
	if err := s.SettleUsage(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleUsage(ctx, second); err != nil {
		t.Fatal(err)
	}
	report, err := s.TaskUsage(ctx, "task")
	if err != nil {
		t.Fatal(err)
	}
	if report.Attempts != 2 || report.TotalTokens != 140 || report.UnknownTokenAttempts != 0 {
		t.Fatalf("incorrect cumulative ledger: %+v", report)
	}
}

// A crash must not refund consumption that may have reached the provider.
func TestInterruptedReservationRemainsUncertain(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.ReserveBudget(ctx, model.BudgetReservation{AttemptID: "a", TaskID: "task", Owner: "old", Status: "active"}, model.BudgetPolicy{MaxAttempts: 2, MaxRuntimeSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInterrupted(ctx, "new"); err != nil {
		t.Fatal(err)
	}
	report, err := s.TaskUsage(ctx, "task")
	if err != nil {
		t.Fatal(err)
	}
	if report.UncertainAttempts != 1 || report.ReservedRuntimeSeconds != 60 {
		t.Fatalf("lost uncertain consumption: %+v", report)
	}
}

// Coding may not consume the attempts/runtime retained for independent verification.
func TestCodingPreservesVerificationCapacity(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	b := model.BudgetPolicy{MaxAttempts: 2, MaxRuntimeSeconds: 60, VerificationAttempts: 1, VerificationRuntimeSeconds: 10}
	r := model.BudgetReservation{AttemptID: "code", TaskID: "task", Owner: "owner", Role: "code"}
	if err := s.ReserveBudget(ctx, r, b); err != nil {
		t.Fatal(err)
	}
	record, _ := s.Get(ctx, "budget_reservations", "code")
	saved, _ := Decode[model.BudgetReservation](record)
	if saved.RuntimeSeconds != 50 {
		t.Fatalf("coding received verification runtime: %+v", saved)
	}
	n := int64(10)
	if err := s.SettleUsage(ctx, model.UsageEntry{AttemptID: "code", TaskID: "task", Role: "code", ElapsedMS: 50000, Usage: &model.Usage{Scope: "attempt", TotalTokens: &n}}); err != nil {
		t.Fatal(err)
	}
	r.AttemptID = "more-code"
	if err := s.ReserveBudget(ctx, r, b); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("verification capacity consumed by coding: %v", err)
	}
	r.AttemptID = "review"
	r.Role = "review"
	if err := s.ReserveBudget(ctx, r, b); err != nil {
		t.Fatalf("reserved verification unavailable: %v", err)
	}
}

// Confirming a crashed child terminated releases concurrency, never its unknown consumption.
func TestUnknownReconciliationRetainsSpendReservation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	b := model.BudgetPolicy{MaxAttempts: 3, MaxRuntimeSeconds: 60, AttemptTokens: 100, AttemptCost: 0.5}
	if err := s.ReserveBudget(ctx, model.BudgetReservation{AttemptID: "a", TaskID: "t", Pool: "p", PoolConcurrency: 1, Owner: "old"}, b); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInterrupted(ctx, "new"); err != nil {
		t.Fatal(err)
	}
	changes, err := s.ReconciliationChanges(ctx, "t", "a", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Write(ctx, changes, nil); err != nil {
		t.Fatal(err)
	}
	report, err := s.TaskUsage(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	if report.UncertainAttempts != 1 || report.ReservedTokens != 100 || report.ReservedCost != 0.5 || report.ReservedRuntimeSeconds != 60 {
		t.Fatalf("unknown consumption refunded: %+v", report)
	}
	if err = s.ReserveBudget(ctx, model.BudgetReservation{AttemptID: "other", TaskID: "other", Pool: "p", PoolConcurrency: 1, Owner: "new"}, b); err != nil {
		t.Fatalf("terminated child retained runner slot: %v", err)
	}
}

// Explicit bounded per-attempt runtime allows safe parallel work without overbooking the total.
func TestParallelReservationsRespectExplicitRuntimeShare(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	b := model.BudgetPolicy{MaxAttempts: 3, MaxRuntimeSeconds: 60, VerificationAttempts: 1, VerificationRuntimeSeconds: 10}
	for _, id := range []string{"a", "b"} {
		if err := s.ReserveBudget(ctx, model.BudgetReservation{AttemptID: id, TaskID: "t", Owner: "o", Role: "code", RuntimeSeconds: 20}, b); err != nil {
			t.Fatalf("explicit parallel share denied: %v", err)
		}
	}
	report, err := s.TaskUsage(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	if report.ReservedRuntimeSeconds != 40 {
		t.Fatalf("whole remaining budget claimed by first attempt: %+v", report)
	}
}

// Deterministic checks consume task runtime but no AI attempts or token measurements.
func TestVerificationRuntimeIsChargedWithoutInferenceCounters(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	b := model.BudgetPolicy{MaxAttempts: 2, MaxRuntimeSeconds: 60, VerificationAttempts: 1, VerificationRuntimeSeconds: 10}
	if err := s.ReserveBudget(ctx, model.BudgetReservation{AttemptID: "checks", TaskID: "t", Owner: "o", Role: "verify", Kind: "verification", RuntimeSeconds: 20}, b); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleUsage(ctx, model.UsageEntry{AttemptID: "checks", TaskID: "t", Role: "verify", Kind: "verification", ElapsedMS: 12000}); err != nil {
		t.Fatal(err)
	}
	report, err := s.TaskUsage(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	if report.Attempts != 0 || report.VerificationRuns != 1 || report.ElapsedMS != 12000 || report.UnknownTokenAttempts != 0 {
		t.Fatalf("checks billed as AI: %+v", report)
	}
}

// Historical attempts without the new ledger are unknown spend, never free new allowance.
func TestLegacySessionsRemainInAdmissionAccounting(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.Put(ctx, "sessions", "old", map[string]any{"task": "t", "provider": "codex", "role": "code", "finished": true, "elapsed_ms": 1000}, 0); err != nil {
		t.Fatal(err)
	}
	report, err := s.TaskUsage(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	if report.Attempts != 1 || report.UnknownTokenAttempts != 1 || report.ElapsedMS != 1000 {
		t.Fatalf("legacy history disappeared: %+v", report)
	}
	b := model.BudgetPolicy{MaxAttempts: 3, MaxRuntimeSeconds: 60, MaxTokens: 100, AttemptTokens: 10}
	if err = s.ReserveBudget(ctx, model.BudgetReservation{AttemptID: "new", TaskID: "t", Owner: "o", Role: "plan"}, b); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("unknown legacy usage admitted under token ceiling: %v", err)
	}
}

// Unknown historical runtime cannot be converted into zero; an explicit operator measurement can settle it.
func TestLegacyRuntimeRequiresExplicitReconciliation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.Put(ctx, "sessions", "old", map[string]any{"task": "t", "provider": "codex", "role": "code", "finished": true}, 0); err != nil {
		t.Fatal(err)
	}
	b := model.BudgetPolicy{MaxAttempts: 3, MaxRuntimeSeconds: 60}
	if err := s.ReserveBudget(ctx, model.BudgetReservation{AttemptID: "new", TaskID: "t", Owner: "o", Role: "plan"}, b); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("unknown runtime treated as0: %v", err)
	}
	n := int64(10)
	elapsed := int64(1000)
	changes, err := s.ReconciliationChanges(ctx, "t", "old", &model.Usage{Scope: "attempt", TotalTokens: &n}, &elapsed)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Write(ctx, changes, nil); err != nil {
		t.Fatal(err)
	}
	report, err := s.TaskUsage(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	if report.Attempts != 1 || report.TotalTokens != 10 || report.UnknownRuntimeAttempts != 0 {
		t.Fatalf("legacy reconciliation doublecounted: %+v", report)
	}
}
