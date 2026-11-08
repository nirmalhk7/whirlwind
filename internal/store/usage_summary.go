package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"whirlwind/internal/model"
)

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
	summarizeUsageEntries(entries, task, seen, &report)
	reservations, err := txValues[model.BudgetReservation](ctx, tx, "budget_reservations")
	if err != nil {
		return report, err
	}
	summarizeTaskReservations(entries, reservations, task, seen, &report)
	if err := addLegacyUsage(ctx, tx, task, seen, &report); err != nil {
		return report, err
	}
	return report, nil
}

func summarizeUsageEntries(entries []model.UsageEntry, task string, seen map[string]bool, report *model.UsageReport) {
	for _, entry := range entries {
		if entry.TaskID != task {
			continue
		}
		report.Entries = append(report.Entries, entry)
		seen[entry.AttemptID] = true
		addUsageEntry(report, entry)
	}
}

func addUsageEntry(report *model.UsageReport, entry model.UsageEntry) {
	switch entry.Kind {
	case "preflight":
		report.PreflightFailures++
	case "verification":
		report.VerificationRuns++
	default:
		report.Attempts++
	}
	report.ElapsedMS += entry.ElapsedMS
	if entry.Kind == "verification" || entry.Kind == "preflight" {
		return
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
		addUsage(report, u)
	}
}

func summarizeTaskReservations(entries []model.UsageEntry, reservations []model.BudgetReservation, task string, seen map[string]bool, report *model.UsageReport) {
	for _, r := range reservations {
		if r.TaskID != task {
			continue
		}
		addTaskReservation(entries, r, seen, report)
	}
}

func addTaskReservation(entries []model.UsageEntry, r model.BudgetReservation, seen map[string]bool, report *model.UsageReport) {
	if !seen[r.AttemptID] {
		countReservationAttempt(report, r)
	}
	seen[r.AttemptID] = true
	if r.RuntimeUnknown {
		report.UnknownRuntimeAttempts++
	}
	if r.Status != "active" && r.Status != "uncertain" && r.Status != "uncertain_released" {
		return
	}
	addReservationStatus(entries, r, report)
	report.ReservedTokens += r.Tokens
	report.ReservedCost += r.Cost
	report.ReservedRuntimeSeconds += r.RuntimeSeconds
}

func countReservationAttempt(report *model.UsageReport, reservation model.BudgetReservation) {
	if reservation.Kind == "verification" {
		report.VerificationRuns++
		return
	}
	report.Attempts++
}

func addReservationStatus(entries []model.UsageEntry, r model.BudgetReservation, report *model.UsageReport) {
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

func seenUsage(entries []model.UsageEntry, id string) bool {
	for _, entry := range entries {
		if entry.AttemptID == id {
			return true
		}
	}
	return false
}
func addLegacyUsage(ctx context.Context, tx *sql.Tx, task string, seen map[string]bool, report *model.UsageReport) (resultErr error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,data FROM resources WHERE kind='sessions' AND json_extract(data,'$.task')=? ORDER BY rowid", task)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
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
