package store

import (
	"context"
	"errors"
	"testing"
	"whirlwind/internal/model"
)

// Live usage beyond the total ceiling must stop the attempt, even before it completes.
func TestLiveUsageStopsAtTaskTokenCeiling(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	budget := model.BudgetPolicy{MaxAttempts: 3, MaxRuntimeSeconds: 60, MaxTokens: 100, AttemptTokens: 50}
	if err := s.ReserveBudget(ctx, model.BudgetReservation{AttemptID: "a", TaskID: "t", Owner: "o"}, budget); err != nil {
		t.Fatal(err)
	}
	n := int64(101)
	entry := model.UsageEntry{AttemptID: "a", TaskID: "t", Provider: "codex", SessionID: "s", Usage: &model.Usage{Scope: "session", TotalTokens: &n}}
	if err := s.CheckUsageLimit(ctx, entry, budget); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("live ceiling ignored: %v", err)
	}
}

// A resumed session with no prior retained baseline must not manufacture precise spend.
func TestResumedUsageWithoutBaselineRemainsUnknown(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n := int64(1000)
	if err := s.SettleUsage(ctx, model.UsageEntry{AttemptID: "a", TaskID: "t", Provider: "codex", SessionID: "historical", Resumed: true, Usage: &model.Usage{Scope: "session", TotalTokens: &n}}); err != nil {
		t.Fatal(err)
	}
	report, err := s.TaskUsage(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalTokens != 0 || report.UnknownTokenAttempts != 1 {
		t.Fatalf("invented attempt tokens: %+v", report)
	}
}

// Live spend cannot consume allowance already reserved by a concurrent sibling.
func TestLiveUsagePreservesConcurrentReservation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	b := model.BudgetPolicy{MaxAttempts: 3, MaxRuntimeSeconds: 60, MaxTokens: 100, AttemptTokens: 50}
	for _, id := range []string{"a", "b"} {
		if err := s.ReserveBudget(ctx, model.BudgetReservation{AttemptID: id, TaskID: "t", Owner: "o", Role: "plan", RuntimeSeconds: 20}, b); err != nil {
			t.Fatal(err)
		}
	}
	n := int64(60)
	if err := s.CheckUsageLimit(ctx, model.UsageEntry{AttemptID: "a", TaskID: "t", Role: "plan", Usage: &model.Usage{Scope: "attempt", TotalTokens: &n}}, b); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("live spend ignored sibling allowance: %v", err)
	}
}
