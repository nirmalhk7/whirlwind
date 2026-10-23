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
func txValues[T any](ctx context.Context, tx *sql.Tx, kind string) ([]T, error) {
	rows, err := tx.QueryContext(ctx, "SELECT data FROM resources WHERE kind=? ORDER BY rowid", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
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

func (s *Store) TaskUsage(ctx context.Context, task string) (model.UsageReport, error) {
	var report model.UsageReport
	err := s.Transaction(ctx, func(tx *sql.Tx) error { var err error; report, err = taskUsageTx(ctx, tx, task); return err })
	return report, err
}
func taskUsageTx(ctx context.Context, tx *sql.Tx, task string) (model.UsageReport, error) {
	report := model.UsageReport{TaskID: task, Entries: []model.UsageEntry{}, UncertainReservations: []model.BudgetReservation{}}
	entries, err := txValues[model.UsageEntry](ctx, tx, "usage")
	if err != nil {
		return report, err
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.TaskID != task {
			continue
		}
		report.Entries = append(report.Entries, entry)
		seen[entry.AttemptID] = true
		if entry.Kind == "preflight" {
			report.PreflightFailures++
		} else if entry.Kind == "verification" {
			report.VerificationRuns++
		} else {
			report.Attempts++
		}
		report.ElapsedMS += entry.ElapsedMS
		if entry.Kind == "verification" || entry.Kind == "preflight" {
			continue
		}
		u := entry.Delta
		if u == nil || u.TotalTokens == nil {
			report.UnknownTokenAttempts++
		} else {
			report.TotalTokens += *u.TotalTokens
		}
		if u == nil || u.Cost == nil {
			report.UnknownCostAttempts++
		} else {
			report.Cost += *u.Cost
		}
		if u != nil {
			addUsage(&report, u)
		}
	}
	reservations, err := txValues[model.BudgetReservation](ctx, tx, "budget_reservations")
	if err != nil {
		return report, err
	}
	for _, r := range reservations {
		if r.TaskID != task {
			continue
		}
		if !seen[r.AttemptID] {
			if r.Kind == "verification" {
				report.VerificationRuns++
			} else {
				report.Attempts++
			}
		}
		seen[r.AttemptID] = true
		if r.RuntimeUnknown {
			report.UnknownRuntimeAttempts++
		}
		if r.Status != "active" && r.Status != "uncertain" && r.Status != "uncertain_released" {
			continue
		}
		if r.Status == "uncertain" || r.Status == "uncertain_released" {
			report.UncertainAttempts++
			if r.Kind != "verification" && !seenUsage(entries, r.AttemptID) {
				report.UnknownTokenAttempts++
				report.UnknownCostAttempts++
			}
			report.UncertainReservations = append(report.UncertainReservations, r)
		} else {
			if r.Kind == "verification" {
				report.ActiveVerificationRuns++
			} else {
				report.ActiveAttempts++
			}
		}
		report.ReservedTokens += r.Tokens
		report.ReservedCost += r.Cost
		report.ReservedRuntimeSeconds += r.RuntimeSeconds
	}
	if err := addLegacyUsage(ctx, tx, task, seen, &report); err != nil {
		return report, err
	}
	return report, nil
}
func addUsage(r *model.UsageReport, u *model.Usage) {
	if u.InputTokens != nil {
		r.InputTokens += *u.InputTokens
	}
	if u.OutputTokens != nil {
		r.OutputTokens += *u.OutputTokens
	}
	if u.CacheWriteTokens != nil {
		r.CacheWriteTokens += *u.CacheWriteTokens
	}
	if u.CachedTokens != nil {
		r.CachedTokens += *u.CachedTokens
	}
}

// CompleteUsage settles usage and its attempt/evidence changes in one SQLite transaction.
func (s *Store) CompleteUsage(ctx context.Context, entry model.UsageEntry, changes []Change) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if _, _, err := txResource[model.UsageEntry](ctx, tx, "usage", entry.AttemptID); err == nil {
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if entry.AttemptID == "" || entry.TaskID == "" {
			return errors.New("usage requires attempt and task identities")
		}
		if entry.RecordedAt == "" {
			entry.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		var err error
		entry.Delta, err = usageDelta(ctx, tx, entry)
		if err != nil {
			return err
		}
		if err = put(ctx, tx, "usage", entry.AttemptID, entry, 0); err != nil {
			return err
		}
		reservation, version, err := txResource[model.BudgetReservation](ctx, tx, "budget_reservations", entry.AttemptID)
		if err == nil {
			reservation.Status = "settled"
			reservation.SettledAt = entry.RecordedAt
			if err = put(ctx, tx, "budget_reservations", entry.AttemptID, reservation, version); err != nil {
				return err
			}
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		for _, change := range changes {
			if err = put(ctx, tx, change.Kind, change.ID, change.Value, change.Expected); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) SettleUsage(ctx context.Context, entry model.UsageEntry) error {
	return s.CompleteUsage(ctx, entry, nil)
}
func usageDelta(ctx context.Context, tx *sql.Tx, entry model.UsageEntry) (*model.Usage, error) {
	if entry.Usage == nil {
		return nil, nil
	}
	if err := validateReportedUsage(entry.Usage); err != nil {
		return nil, err
	}
	if entry.Usage.Scope != "attempt" && entry.Usage.Scope != "session" {
		return nil, nil
	}
	u := *entry.Usage
	u.Scope = "attempt"
	if entry.Usage.Scope != "session" {
		return &u, nil
	}
	u.Raw = nil
	entries, err := txValues[model.UsageEntry](ctx, tx, "usage")
	if err != nil {
		return nil, err
	}
	var previous *model.Usage
	for _, old := range entries {
		if old.Provider == entry.Provider && old.SessionID == entry.SessionID && entry.SessionID != "" && old.Model == entry.Model && entry.Model != "" && old.Transport == entry.Transport && old.ExecutionProfile == entry.ExecutionProfile && old.PlanVersion == entry.PlanVersion && old.Usage != nil && old.Usage.Scope == "session" {
			previous = old.Usage
		}
	}
	if previous == nil {
		if entry.Resumed || entry.SessionID == "" {
			return nil, nil
		}
		return &u, nil
	}
	u.TotalTokens = deltaInt(u.TotalTokens, previous.TotalTokens)
	u.InputTokens = deltaInt(u.InputTokens, previous.InputTokens)
	u.OutputTokens = deltaInt(u.OutputTokens, previous.OutputTokens)
	u.CachedTokens = deltaInt(u.CachedTokens, previous.CachedTokens)
	u.CacheWriteTokens = deltaInt(u.CacheWriteTokens, previous.CacheWriteTokens)
	if u.Cost == nil || previous.Cost == nil || *u.Cost < *previous.Cost {
		u.Cost = nil
	} else {
		v := *u.Cost - *previous.Cost
		u.Cost = &v
	}
	return &u, nil
}
func deltaInt(current, prior *int64) *int64 {
	if current == nil || prior == nil || *current < *prior {
		return nil
	}
	v := *current - *prior
	return &v
}

func seenUsage(entries []model.UsageEntry, id string) bool {
	for _, entry := range entries {
		if entry.AttemptID == id {
			return true
		}
	}
	return false
}
func addLegacyUsage(ctx context.Context, tx *sql.Tx, task string, seen map[string]bool, report *model.UsageReport) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,data FROM resources WHERE kind='sessions' AND json_extract(data,'$.task')=? ORDER BY rowid", task)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return err
		}
		if seen[id] {
			continue
		}
		var attempt model.Attempt
		if err = json.Unmarshal(raw, &attempt); err != nil {
			return err
		}
		report.Attempts++
		report.UnknownTokenAttempts++
		report.UnknownCostAttempts++
		entry := model.UsageEntry{Kind: "legacy", AttemptID: id, TaskID: task, ProjectID: attempt.ProjectID, Provider: attempt.Provider, Model: attempt.Model, Role: attempt.Role, SessionID: attempt.SessionID, Usage: attempt.Usage}
		if attempt.ElapsedMS != nil && *attempt.ElapsedMS >= 0 {
			entry.ElapsedMS = *attempt.ElapsedMS
			report.ElapsedMS += *attempt.ElapsedMS
		} else {
			report.UnknownRuntimeAttempts++
		}
		report.Entries = append(report.Entries, entry)
	}
	return rows.Err()
}
