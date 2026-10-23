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
		r, version, err := txResource[model.BudgetReservation](ctx, tx, "budget_reservations", attempt)
		if errors.Is(err, ErrNotFound) {
			legacy, _, legacyErr := txResource[model.Attempt](ctx, tx, "sessions", attempt)
			if legacyErr != nil {
				return legacyErr
			}
			if legacy.Task != task {
				return ErrNotFound
			}
			if !legacy.Finished {
				return errors.New("legacy provider attempt must be terminated before reconciliation")
			}
			r = model.BudgetReservation{AttemptID: attempt, TaskID: task, Provider: legacy.Provider, Model: legacy.Model, Role: legacy.Role, Owner: legacy.Owner, Status: "uncertain", RuntimeUnknown: legacy.ElapsedMS == nil}
			if legacy.ElapsedMS != nil && *legacy.ElapsedMS > 0 {
				r.RuntimeSeconds = int((*legacy.ElapsedMS + 999) / 1000)
			}
		} else if err != nil {
			return err
		}
		if r.TaskID != task {
			return ErrNotFound
		}
		if r.Status != "uncertain" && r.Status != "uncertain_released" {
			return errors.New("only interrupted uncertain reservations can be reconciled")
		}
		r.Status = "uncertain_released"
		if usage == nil {
			changes = append(changes, Change{Kind: "budget_reservations", ID: attempt, Value: r, Expected: version})
			return nil
		}
		if usage.Scope != "attempt" {
			return errors.New("operator reconciliation requires attempt-scoped usage")
		}
		if elapsed == nil || *elapsed < 0 {
			return errors.New("known usage reconciliation requires nonnegative active_runtime_ms")
		}
		if err = validateReportedUsage(usage); err != nil {
			return err
		}
		entry, entryVersion, err := txResource[model.UsageEntry](ctx, tx, "usage", attempt)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		entry.Kind = r.Kind
		entry.AttemptID, entry.TaskID, entry.Provider, entry.Model, entry.Role = r.AttemptID, r.TaskID, r.Provider, r.Model, r.Role
		value := *usage
		value.Source = "local_operator"
		entry.Usage = &value
		entry.Delta = &value
		entry.ElapsedMS = *elapsed
		entry.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
		r.RuntimeUnknown = false
		r.RuntimeSeconds = 0
		if usage.TotalTokens != nil {
			r.Tokens = 0
		}
		if usage.Cost != nil {
			r.Cost = 0
		}
		if r.Tokens == 0 && r.Cost == 0 {
			r.Status = "settled"
			r.SettledAt = entry.RecordedAt
		}
		changes = append(changes, Change{Kind: "usage", ID: attempt, Value: entry, Expected: entryVersion}, Change{Kind: "budget_reservations", ID: attempt, Value: r, Expected: version})
		return nil
	})
	return changes, err
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
