package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"whirlwind/internal/model"
)

type verificationReserve struct {
	tokens int64
	cost   float64
}

// ReserveBudget serializes task and shared-pool admission before any inference starts.
func (s *Store) ReserveBudget(ctx context.Context, r model.BudgetReservation, budget model.BudgetPolicy) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		alreadyReserved, effectiveBudget, err := reservationBudget(ctx, tx, r, budget)
		if err != nil || alreadyReserved {
			return err
		}
		budget = effectiveBudget
		if err := budget.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrBudgetExceeded, err)
		}
		report, err := taskUsageTx(ctx, tx, r.TaskID)
		if err != nil {
			return err
		}
		remaining, reserve, err := validateRuntimeBudget(r, budget, report)
		if err != nil {
			return err
		}
		return persistBudgetReservation(budgetReservationInput{ctx: ctx, tx: tx, reservation: r, budget: budget, report: report, remaining: remaining, reserve: reserve})
	})
}

type budgetReservationInput struct {
	ctx         context.Context
	tx          *sql.Tx
	reservation model.BudgetReservation
	budget      model.BudgetPolicy
	report      model.UsageReport
	remaining   int
	reserve     verificationReserve
}

func persistBudgetReservation(input budgetReservationInput) error {
	ctx, tx, reservation := input.ctx, input.tx, input.reservation
	budget, report, remaining, reserve := input.budget, input.report, input.remaining, input.reserve
	reservation.Tokens = budget.AttemptTokens
	reservation.Cost = budget.AttemptCost
	if reservation.Kind == "verification" {
		reservation.Tokens = 0
		reservation.Cost = 0
	}
	if reservation.RuntimeSeconds <= 0 || reservation.RuntimeSeconds > remaining {
		reservation.RuntimeSeconds = remaining
	}
	if err := validateTokenCostBudget(reservation, budget, report, reserve); err != nil {
		return err
	}
	if err := validatePoolCapacity(ctx, tx, reservation); err != nil {
		return err
	}
	if reservation.AttemptID == "" || reservation.TaskID == "" {
		return errors.New("reservation requires attempt and task identities")
	}
	reservation.Status = "active"
	reservation.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return put(ctx, tx, "budget_reservations", reservation.AttemptID, reservation, 0)
}

func reservationBudget(ctx context.Context, tx *sql.Tx, reservation model.BudgetReservation, budget model.BudgetPolicy) (bool, model.BudgetPolicy, error) {
	if old, _, err := txResource[model.BudgetReservation](ctx, tx, "budget_reservations", reservation.AttemptID); err == nil {
		if old.TaskID != reservation.TaskID || old.Owner != reservation.Owner {
			return false, budget, ErrConflict
		}
		return true, budget, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, budget, err
	}
	if task, _, err := txResource[model.Task](ctx, tx, "tasks", reservation.TaskID); err == nil && task.Budget != nil && reservation.Role != "ideate" {
		budget = *task.Budget
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return false, budget, err
	}
	return false, budget, nil
}

func validateRuntimeBudget(reservation model.BudgetReservation, budget model.BudgetPolicy, report model.UsageReport) (int, verificationReserve, error) {
	if report.UnknownRuntimeAttempts > 0 {
		return 0, verificationReserve{}, fmt.Errorf("%w: historical active runtime unknown; reconcile retained attempts before continuing", ErrBudgetExceeded)
	}
	remaining := budget.MaxRuntimeSeconds - int((report.ElapsedMS+999)/1000) - report.ReservedRuntimeSeconds
	if (reservation.Kind != "verification" && report.Attempts >= budget.MaxAttempts) || remaining <= 0 {
		return 0, verificationReserve{}, fmt.Errorf("%w: attempt or active runtime ceiling reached", ErrBudgetExceeded)
	}
	reserve := verificationReserve{}
	if reservation.Role == "code" || reservation.Kind == "verification" {
		if budget.VerificationAttempts <= 0 || budget.VerificationRuntimeSeconds <= 0 {
			return 0, verificationReserve{}, fmt.Errorf("%w: configure verification_attempts and verification_runtime_seconds before implementation", ErrBudgetExceeded)
		}
		if (reservation.Kind != "verification" && report.Attempts+budget.VerificationAttempts >= budget.MaxAttempts) || remaining <= budget.VerificationRuntimeSeconds {
			return 0, verificationReserve{}, fmt.Errorf("%w: remaining capacity reserved for verification", ErrBudgetExceeded)
		}
		remaining -= budget.VerificationRuntimeSeconds
	}
	if reservation.Role == "code" {
		reserve.tokens = int64(budget.VerificationAttempts) * budget.AttemptTokens
		reserve.cost = float64(budget.VerificationAttempts) * budget.AttemptCost
	}
	return remaining, reserve, nil
}

func validateTokenCostBudget(reservation model.BudgetReservation, budget model.BudgetPolicy, report model.UsageReport, reserve verificationReserve) error {
	if reservation.Kind != "verification" && budget.MaxTokens > 0 && (report.UnknownTokenAttempts > 0 || report.UncertainAttempts > 0 || report.TotalTokens+report.ReservedTokens+reservation.Tokens+reserve.tokens > budget.MaxTokens) {
		return fmt.Errorf("%w: token ceiling or unknown prior consumption", ErrBudgetExceeded)
	}
	if reservation.Kind != "verification" && budget.MaxCost > 0 && (report.UnknownCostAttempts > 0 || report.UncertainAttempts > 0 || report.Cost+report.ReservedCost+reservation.Cost+reserve.cost > budget.MaxCost) {
		return fmt.Errorf("%w: cost ceiling or unknown prior consumption", ErrBudgetExceeded)
	}
	return nil
}

func validatePoolCapacity(ctx context.Context, tx *sql.Tx, reservation model.BudgetReservation) error {
	if reservation.Pool == "" || reservation.PoolConcurrency <= 0 {
		return nil
	}
	all, err := txValues[model.BudgetReservation](ctx, tx, "budget_reservations")
	if err != nil {
		return err
	}
	count := 0
	for _, old := range all {
		if old.Pool == reservation.Pool && (old.Status == "active" || old.Status == "uncertain") {
			count++
		}
	}
	if count >= reservation.PoolConcurrency {
		return ErrPoolBusy
	}
	return nil
}

// CheckUsageLimit evaluates live reported usage without settling the attempt.
