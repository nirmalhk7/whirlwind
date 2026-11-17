package store

import (
	"context"
	"database/sql"
	"time"

	"whirlwind/internal/model"
)

type LocalDeliveryOwner struct {
	Owner      string
	Generation int
}

// CommitLocalDelivery holds a SQLite writer lock across the bounded local ref
// publication. Approval revocation and stale attempt ownership cannot race past
// the authorization check. A lost commit reconciles the same CAS-created ref.
func (s *Store) CommitLocalDelivery(ctx context.Context, planRecord, attemptRecord model.Record, ownership LocalDeliveryOwner, key string, receipt model.LocalFinalization, publish func() error) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		locked, err := tx.ExecContext(ctx, "UPDATE resources SET version=version WHERE kind='execution_plans' AND id=? AND version=?", planRecord.ID, planRecord.Version)
		if err != nil {
			return err
		}
		if err = requireOneRow(locked); err != nil {
			return err
		}
		attempt, version, err := txResource[model.Attempt](ctx, tx, "sessions", attemptRecord.ID)
		if err != nil {
			return err
		}
		expires, parseErr := time.Parse(time.RFC3339Nano, attempt.LeaseExpiresAt)
		deadline, deadlineErr := time.Parse(time.RFC3339Nano, attempt.DeadlineAt)
		if version != attemptRecord.Version || attempt.Owner != ownership.Owner || attempt.ExecutionGeneration != ownership.Generation || attempt.Finished || attempt.Status != "running" || parseErr != nil || !expires.After(time.Now()) || deadlineErr != nil || !deadline.After(time.Now()) || attempt.Task != receipt.PlanID || attempt.ProjectID != receipt.ProjectID || attempt.PlanVersion != receipt.PlanVersion || attempt.Role != "finalizer" || attempt.Purpose != "local_finalization" {
			return ErrConflict
		}
		if err := publish(); err != nil {
			return err
		}
		return put(ctx, tx, "finalizations", key, receipt, 0)
	})
}
