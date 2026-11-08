package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"whirlwind/internal/model"
)

var ErrBudgetExceeded = errors.New("task budget admission denied")
var ErrPoolBusy = errors.New("quota pool concurrency reserved")

func (s *Store) CheckUsageLimit(ctx context.Context, entry model.UsageEntry, budget model.BudgetPolicy) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error { return checkUsageLimitTx(ctx, tx, entry, budget) })
}

func checkUsageLimitTx(ctx context.Context, tx *sql.Tx, entry model.UsageEntry, budget model.BudgetPolicy) error {
	effectiveBudget, err := taskBudgetForRole(ctx, tx, entry.TaskID, entry.Role, budget)
	if err != nil {
		return err
	}
	budget = effectiveBudget
	report, delta, reservation, err := currentUsageLimit(ctx, tx, entry)
	if err != nil || delta == nil {
		return err
	}
	return validateLiveUsage(entry, budget, report, delta, reservation)
}

func taskBudgetForRole(ctx context.Context, tx *sql.Tx, taskID, role string, budget model.BudgetPolicy) (model.BudgetPolicy, error) {
	task, _, err := txResource[model.Task](ctx, tx, "tasks", taskID)
	if errors.Is(err, ErrNotFound) {
		return budget, nil
	}
	if err != nil {
		return budget, err
	}
	if task.Budget != nil && role != "ideate" {
		budget = *task.Budget
	}
	return budget, nil
}

func currentUsageLimit(ctx context.Context, tx *sql.Tx, entry model.UsageEntry) (model.UsageReport, *model.Usage, model.BudgetReservation, error) {
	report, err := taskUsageTx(ctx, tx, entry.TaskID)
	if err != nil {
		return report, nil, model.BudgetReservation{}, err
	}
	delta, err := usageDelta(ctx, tx, entry)
	if err != nil || delta == nil {
		return report, delta, model.BudgetReservation{}, err
	}
	reservation, _, err := txResource[model.BudgetReservation](ctx, tx, "budget_reservations", entry.AttemptID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return report, nil, reservation, err
	}
	return report, delta, reservation, nil
}

func validateLiveUsage(entry model.UsageEntry, budget model.BudgetPolicy, report model.UsageReport, delta *model.Usage, reservation model.BudgetReservation) error {
	otherTokens := max(int64(0), report.ReservedTokens-reservation.Tokens)
	otherCost := max(float64(0), report.ReservedCost-reservation.Cost)
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
}
