package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"whirlwind/internal/model"
)

type directionRemoval struct{}

// RemoveDirection participates in the caller's batch so task pauses, all four
// deletions, events and the idempotency receipt commit together.
func (s *Store) RemoveDirection(ctx context.Context, id string, expected int) error {
	if b := staged(ctx); b != nil {
		if err := b.put(ctx, s, Change{"directions", id, directionRemoval{}, expected}); err != nil {
			return err
		}
		b.records["directions:"+id] = model.Record{ID: id}
		return nil
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error { return removeDirection(ctx, tx, id, expected) })
}

func removeDirection(ctx context.Context, tx *sql.Tx, id string, expected int) error {
	var data []byte
	if err := tx.QueryRowContext(ctx, "SELECT data FROM resources WHERE kind='directions' AND id=? AND version=?", id, expected).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return err
	}
	var direction model.Direction
	if err := json.Unmarshal(data, &direction); err != nil {
		return err
	}
	if direction.ProjectID != "" {
		proposal, err := loadProposalTx(tx, direction.ProjectID, id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) {
			proposal = proposalFromDirection(direction, "")
		}
		proposal.Status = "removed"
		proposal.DeferralReason = "Removed from active directions by the user"
		if proposal.CreatedAt == "" {
			proposal.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		if err := writeProposalTx(tx, proposal, false); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM resources WHERE kind='directions' AND id=? AND version=?", id, expected)
	if err != nil {
		return err
	}
	if err = requireOneRow(result); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO events(kind,resource_id,data,created_at) VALUES('directions',?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))", id, `{"deleted":true}`)
	return err
}
