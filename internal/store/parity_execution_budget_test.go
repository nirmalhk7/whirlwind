package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"whirlwind/internal/model"
)

func TestParityExecutionContinuesBeyondWholePlanAttemptAndRuntimeCeilings(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "execution_plans", "plan", model.ExecutionPlan{ID: "plan", IdeaID: "i", ProjectID: "p", IdeationRunID: "source", Stage: "coding"}, 0); err != nil {
		t.Fatal(err)
	}
	// Prior fixed ceilings must not impose a Coding/Reviewer iteration cap.
	budget := model.BudgetPolicy{MaxAttempts: 2, MaxRuntimeSeconds: 1}
	for n := 0; n < 8; n++ {
		id := fmt.Sprintf("attempt-%d", n)
		if err := s.ReserveBudget(ctx, model.BudgetReservation{AttemptID: id, TaskID: "plan", Owner: "supervisor", Role: "review", RuntimeSeconds: 300, Pool: "provider", PoolConcurrency: 1}, budget); err != nil {
			t.Fatalf("plan stopped at iteration %d: %v", n, err)
		}
		r, err := s.Get(ctx, "budget_reservations", id)
		if err != nil {
			t.Fatal(err)
		}
		reservation, _ := Decode[model.BudgetReservation](r)
		if reservation.RuntimeSeconds != 300 {
			t.Fatalf("whole-plan budget shortened hard attempt allowance: %d", reservation.RuntimeSeconds)
		}
		if err := s.ReserveBudget(ctx, model.BudgetReservation{AttemptID: id + "-peer", TaskID: "plan", Owner: "supervisor", Role: "review", RuntimeSeconds: 300, Pool: "provider", PoolConcurrency: 1}, budget); !errors.Is(err, ErrPoolBusy) {
			t.Fatalf("quota pool concurrency lost: %v", err)
		}
		if err := s.SettleUsage(ctx, model.UsageEntry{AttemptID: id, TaskID: "plan", Role: "review", ElapsedMS: 2000}); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := s.TaskUsage(ctx, "plan")
	if err != nil || usage.Attempts != 8 || usage.ElapsedMS != 16000 {
		t.Fatalf("unbounded loop lost usage accounting: %+v %v", usage, err)
	}
}
