package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"whirlwind/internal/model"
)

func txResource[T any](ctx context.Context, tx *sql.Tx, kind, id string) (T, int, error) {
	var value T
	var raw []byte
	var version int
	err := tx.QueryRowContext(ctx, "SELECT data,version FROM resources WHERE kind=? AND id=?", kind, id).Scan(&raw, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return value, 0, ErrNotFound
	}
	if err != nil {
		return value, 0, err
	}
	err = json.Unmarshal(raw, &value)
	return value, version, err
}
func txValues[T any](ctx context.Context, tx *sql.Tx, kind string) (out []T, resultErr error) {
	rows, err := tx.QueryContext(ctx, "SELECT data FROM resources WHERE kind=? ORDER BY rowid", kind)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	out = []T{}
	for rows.Next() {
		var raw []byte
		var value T
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) CompleteUsage(ctx context.Context, entry model.UsageEntry, changes []Change) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error { return completeUsageTx(ctx, tx, entry, changes) })
}

func completeUsageTx(ctx context.Context, tx *sql.Tx, entry model.UsageEntry, changes []Change) error {
	exists, err := usageEntryExists(ctx, tx, entry.AttemptID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if err := prepareUsageEntry(&entry); err != nil {
		return err
	}
	if err := persistCompletedUsage(ctx, tx, entry); err != nil {
		return err
	}
	return applyUsageChanges(ctx, tx, changes)
}

func prepareUsageEntry(entry *model.UsageEntry) error {
	if entry.AttemptID == "" || entry.TaskID == "" {
		return errors.New("usage requires attempt and task identities")
	}
	if entry.RecordedAt == "" {
		entry.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return nil
}

func persistCompletedUsage(ctx context.Context, tx *sql.Tx, entry model.UsageEntry) error {
	if err := recordUsageEntry(ctx, tx, entry); err != nil {
		return err
	}
	if err := settleBudgetReservation(ctx, tx, entry); err != nil {
		return err
	}
	return nil
}

func usageEntryExists(ctx context.Context, tx *sql.Tx, attemptID string) (bool, error) {
	_, _, err := txResource[model.UsageEntry](ctx, tx, "usage", attemptID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, err
}

func recordUsageEntry(ctx context.Context, tx *sql.Tx, entry model.UsageEntry) error {
	delta, err := usageDelta(ctx, tx, entry)
	if err != nil {
		return err
	}
	entry.Delta = delta
	return put(ctx, tx, "usage", entry.AttemptID, entry, 0)
}

func settleBudgetReservation(ctx context.Context, tx *sql.Tx, entry model.UsageEntry) error {
	reservation, version, err := txResource[model.BudgetReservation](ctx, tx, "budget_reservations", entry.AttemptID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	reservation.Status = "settled"
	reservation.SettledAt = entry.RecordedAt
	return put(ctx, tx, "budget_reservations", entry.AttemptID, reservation, version)
}

func applyUsageChanges(ctx context.Context, tx *sql.Tx, changes []Change) error {
	for _, change := range changes {
		if err := put(ctx, tx, change.Kind, change.ID, change.Value, change.Expected); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) SettleUsage(ctx context.Context, entry model.UsageEntry) error {
	return s.CompleteUsage(ctx, entry, nil)
}
