package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
	"whirlwind/internal/model"
)

// ReconciliationChanges prepares version-fenced changes so the API can commit them with its mutation receipt.
func (s *Store) ReconciliationChanges(ctx context.Context, task, attempt string, usage *model.Usage, elapsed *int64) ([]Change, error) {
	changes := []Change{}
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		changes, err = prepareReconciliationChanges(ctx, tx, task, attempt, usage, elapsed)
		return err
	})
	return changes, err
}

func prepareReconciliationChanges(ctx context.Context, tx *sql.Tx, task, attempt string, usage *model.Usage, elapsed *int64) ([]Change, error) {
	reservation, version, err := reconciliationReservation(ctx, tx, task, attempt)
	if err != nil {
		return nil, err
	}
	if err := validateReconciliationReservation(reservation, task); err != nil {
		return nil, err
	}
	reservation.Status = "uncertain_released"
	if usage == nil {
		return []Change{{Kind: "budget_reservations", ID: attempt, Value: reservation, Expected: version}}, nil
	}
	entry, entryVersion, err := reconciledUsageEntry(reconciledUsageInput{ctx: ctx, tx: tx, attempt: attempt, usage: usage, elapsed: elapsed, reservation: &reservation})
	if err != nil {
		return nil, err
	}
	return []Change{{Kind: "usage", ID: attempt, Value: entry, Expected: entryVersion}, {Kind: "budget_reservations", ID: attempt, Value: reservation, Expected: version}}, nil
}

func validateReconciliationReservation(reservation model.BudgetReservation, task string) error {
	if reservation.TaskID != task {
		return ErrNotFound
	}
	if reservation.Status != "uncertain" && reservation.Status != "uncertain_released" {
		return errors.New("only interrupted uncertain reservations can be reconciled")
	}
	return nil
}

func reconciliationReservation(ctx context.Context, tx *sql.Tx, task, attempt string) (model.BudgetReservation, int, error) {
	reservation, version, err := txResource[model.BudgetReservation](ctx, tx, "budget_reservations", attempt)
	if !errors.Is(err, ErrNotFound) {
		return reservation, version, err
	}
	legacy, _, err := txResource[model.Attempt](ctx, tx, "sessions", attempt)
	if err != nil {
		return model.BudgetReservation{}, 0, err
	}
	if legacy.Task != task {
		return model.BudgetReservation{}, 0, ErrNotFound
	}
	if !legacy.Finished {
		return model.BudgetReservation{}, 0, errors.New("legacy provider attempt must be terminated before reconciliation")
	}
	reservation = model.BudgetReservation{AttemptID: attempt, TaskID: task, Provider: legacy.Provider, Model: legacy.Model, Role: legacy.Role, Owner: legacy.Owner, Status: "uncertain", RuntimeUnknown: legacy.ElapsedMS == nil}
	if legacy.ElapsedMS != nil && *legacy.ElapsedMS > 0 {
		reservation.RuntimeSeconds = int((*legacy.ElapsedMS + 999) / 1000)
	}
	return reservation, 0, nil
}

type reconciledUsageInput struct {
	ctx         context.Context
	tx          *sql.Tx
	attempt     string
	usage       *model.Usage
	elapsed     *int64
	reservation *model.BudgetReservation
}

func reconciledUsageEntry(input reconciledUsageInput) (model.UsageEntry, int, error) {
	ctx, tx, attempt := input.ctx, input.tx, input.attempt
	usage, elapsed, reservation := input.usage, input.elapsed, input.reservation
	if usage.Scope != "attempt" {
		return model.UsageEntry{}, 0, errors.New("operator reconciliation requires attempt-scoped usage")
	}
	if elapsed == nil || *elapsed < 0 {
		return model.UsageEntry{}, 0, errors.New("known usage reconciliation requires nonnegative active_runtime_ms")
	}
	if err := validateReportedUsage(usage); err != nil {
		return model.UsageEntry{}, 0, err
	}
	entry, version, err := txResource[model.UsageEntry](ctx, tx, "usage", attempt)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return model.UsageEntry{}, 0, err
	}
	entry.Kind = reservation.Kind
	entry.AttemptID, entry.TaskID, entry.Provider, entry.Model, entry.Role = reservation.AttemptID, reservation.TaskID, reservation.Provider, reservation.Model, reservation.Role
	value := *usage
	value.Source = "local_operator"
	entry.Usage = &value
	entry.Delta = &value
	entry.ElapsedMS = *elapsed
	entry.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	reservation.RuntimeUnknown = false
	reservation.RuntimeSeconds = 0
	if usage.TotalTokens != nil {
		reservation.Tokens = 0
	}
	if usage.Cost != nil {
		reservation.Cost = 0
	}
	if reservation.Tokens == 0 && reservation.Cost == 0 {
		reservation.Status = "settled"
		reservation.SettledAt = entry.RecordedAt
	}
	return entry, version, nil
}
func validateReportedUsage(u *model.Usage) error {
	for _, n := range []*int64{u.InputTokens, u.OutputTokens, u.CachedTokens, u.CacheWriteTokens, u.TotalTokens} {
		if n != nil && *n < 0 {
			return errors.New("usage counters must be nonnegative")
		}
	}
	if u.Cost != nil && (math.IsNaN(*u.Cost) || math.IsInf(*u.Cost, 0) || *u.Cost < 0) {
		return fmt.Errorf("usage cost must be finite and nonnegative")
	}
	return nil
}
