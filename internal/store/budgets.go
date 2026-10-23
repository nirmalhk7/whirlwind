package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"whirlwind/internal/model"
)

var ErrBudgetExceeded = errors.New("task budget admission denied")
var ErrPoolBusy = errors.New("quota pool concurrency reserved")

// ReserveBudget serializes task and shared-pool admission before any inference starts.
func (s *Store) ReserveBudget(ctx context.Context, r model.BudgetReservation, budget model.BudgetPolicy) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if old, _, err := txResource[model.BudgetReservation](ctx, tx, "budget_reservations", r.AttemptID); err == nil {
			if old.TaskID != r.TaskID || old.Owner != r.Owner {
				return ErrConflict
			}
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if task, _, err := txResource[model.Task](ctx, tx, "tasks", r.TaskID); err == nil && task.Budget != nil && r.Role != "ideate" {
			budget = *task.Budget
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := budget.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrBudgetExceeded, err)
		}
		report, err := taskUsageTx(ctx, tx, r.TaskID)
		if err != nil {
			return err
		}
		if report.UnknownRuntimeAttempts > 0 {
			return fmt.Errorf("%w: historical active runtime unknown; reconcile retained attempts before continuing", ErrBudgetExceeded)
		}
		remaining := budget.MaxRuntimeSeconds - int((report.ElapsedMS+999)/1000) - report.ReservedRuntimeSeconds
		if (r.Kind != "verification" && report.Attempts >= budget.MaxAttempts) || remaining <= 0 {
			return fmt.Errorf("%w: attempt or active runtime ceiling reached", ErrBudgetExceeded)
		}
		if r.Role == "code" || r.Kind == "verification" {
			if budget.VerificationAttempts <= 0 || budget.VerificationRuntimeSeconds <= 0 {
				return fmt.Errorf("%w: configure verification_attempts and verification_runtime_seconds before implementation", ErrBudgetExceeded)
			}
			if (r.Kind != "verification" && report.Attempts+budget.VerificationAttempts >= budget.MaxAttempts) || remaining <= budget.VerificationRuntimeSeconds {
				return fmt.Errorf("%w: remaining capacity reserved for verification", ErrBudgetExceeded)
			}
			remaining -= budget.VerificationRuntimeSeconds
		}
		r.Tokens = budget.AttemptTokens
		r.Cost = budget.AttemptCost
		if r.Kind == "verification" {
			r.Tokens = 0
			r.Cost = 0
		}
		if r.RuntimeSeconds <= 0 || r.RuntimeSeconds > remaining {
			r.RuntimeSeconds = remaining
		}
		verificationTokens := int64(0)
		verificationCost := float64(0)
		if r.Role == "code" {
			verificationTokens = int64(budget.VerificationAttempts) * budget.AttemptTokens
			verificationCost = float64(budget.VerificationAttempts) * budget.AttemptCost
		}
		if r.Kind != "verification" && budget.MaxTokens > 0 && (report.UnknownTokenAttempts > 0 || report.UncertainAttempts > 0 || report.TotalTokens+report.ReservedTokens+r.Tokens+verificationTokens > budget.MaxTokens) {
			return fmt.Errorf("%w: token ceiling or unknown prior consumption", ErrBudgetExceeded)
		}
		if r.Kind != "verification" && budget.MaxCost > 0 && (report.UnknownCostAttempts > 0 || report.UncertainAttempts > 0 || report.Cost+report.ReservedCost+r.Cost+verificationCost > budget.MaxCost) {
			return fmt.Errorf("%w: cost ceiling or unknown prior consumption", ErrBudgetExceeded)
		}
		if r.Pool != "" && r.PoolConcurrency > 0 {
			all, err := txValues[model.BudgetReservation](ctx, tx, "budget_reservations")
			if err != nil {
				return err
			}
			n := 0
			for _, old := range all {
				if old.Pool == r.Pool && (old.Status == "active" || old.Status == "uncertain") {
					n++
				}
			}
			if n >= r.PoolConcurrency {
				return ErrPoolBusy
			}
		}
		if r.AttemptID == "" || r.TaskID == "" {
			return errors.New("reservation requires attempt and task identities")
		}
		r.Status = "active"
		r.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return put(ctx, tx, "budget_reservations", r.AttemptID, r, 0)
	})
}

// CheckUsageLimit evaluates live reported usage without settling the attempt.
func (s *Store) CheckUsageLimit(ctx context.Context, entry model.UsageEntry, budget model.BudgetPolicy) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if task, _, err := txResource[model.Task](ctx, tx, "tasks", entry.TaskID); err == nil && task.Budget != nil && entry.Role != "ideate" {
			budget = *task.Budget
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		report, err := taskUsageTx(ctx, tx, entry.TaskID)
		if err != nil {
			return err
		}
		delta, err := usageDelta(ctx, tx, entry)
		if err != nil {
			return err
		}
		if delta == nil {
			return nil
		}
		own, _, getErr := txResource[model.BudgetReservation](ctx, tx, "budget_reservations", entry.AttemptID)
		if getErr != nil && !errors.Is(getErr, ErrNotFound) {
			return getErr
		}
		otherTokens := max(int64(0), report.ReservedTokens-own.Tokens)
		otherCost := max(float64(0), report.ReservedCost-own.Cost)
		if entry.Role == "code" {
			otherTokens += int64(budget.VerificationAttempts) * budget.AttemptTokens
			otherCost += float64(budget.VerificationAttempts) * budget.AttemptCost
		}

		if budget.MaxTokens > 0 && delta.TotalTokens != nil && report.TotalTokens+otherTokens+*delta.TotalTokens >= budget.MaxTokens {
			return fmt.Errorf("%w: live token ceiling reached", ErrBudgetExceeded)
		}
		if budget.MaxCost > 0 && delta.Cost != nil && report.Cost+otherCost+*delta.Cost >= budget.MaxCost {
			return fmt.Errorf("%w: live cost ceiling reached", ErrBudgetExceeded)
		}
		return nil
	})
}
