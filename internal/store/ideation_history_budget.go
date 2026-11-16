package store

import (
	"context"
	"database/sql"
	"errors"
)

var ErrHistoryBudget = errors.New("ideation history budget exhausted")

// History limits are pinned once for the existing ideation run. Debits commit
// before context is handed to an agent and survive process loss.
func (s *Store) InitializeIdeationHistoryBudget(ctx context.Context, runID, projectID string, limit int) (remaining int, err error) {
	if limit <= 0 {
		return 0, ErrConflict
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `UPDATE ideation_runs SET history_token_limit=? WHERE run_id=? AND project_id=? AND (history_token_limit=0 OR history_token_limit=?) RETURNING history_token_limit-history_tokens_used`, limit, runID, projectID, limit).Scan(&remaining)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return err
	})
	return remaining, err
}
func (s *Store) ConsumeIdeationHistory(ctx context.Context, runID, projectID string, amount int) (remaining int, err error) {
	if amount < 0 {
		return 0, ErrConflict
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `UPDATE ideation_runs SET history_tokens_used=history_tokens_used+? WHERE run_id=? AND project_id=? AND history_token_limit>0 AND history_tokens_used+?<=history_token_limit RETURNING history_token_limit-history_tokens_used`, amount, runID, projectID, amount).Scan(&remaining)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHistoryBudget
		}
		return err
	})
	return remaining, err
}
