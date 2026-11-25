package store

import (
	"context"
	"database/sql"
	"errors"
)

type resourceVersionAssertion struct{}

// VersionPrecondition guards a read dependency in the same transaction as writes.
// An expected version of zero requires that the record still be absent.
func VersionPrecondition(kind, id string, expected int) Change {
	return Change{Kind: kind, ID: id, Value: resourceVersionAssertion{}, Expected: expected}
}

func (c Change) isVersionPrecondition() bool {
	_, ok := c.Value.(resourceVersionAssertion)
	return ok
}

// AssertVersion adds a read precondition to the request's commit. It does not
// rewrite the inspected resource or manufacture a new version/event.
func (s *Store) AssertVersion(ctx context.Context, kind, id string, expected int) error {
	batch := staged(ctx)
	if batch == nil {
		return errors.New("resource version assertion requires a staged mutation")
	}
	record, err := s.Get(ctx, kind, id)
	if err != nil {
		return err
	}
	if expected < 1 || record.Version != expected {
		return ErrConflict
	}
	batch.changes = append(batch.changes, Change{Kind: kind, ID: id, Value: resourceVersionAssertion{}, Expected: expected})
	return nil
}

func assertResourceVersionTx(ctx context.Context, tx *sql.Tx, kind, id string, expected int) error {
	var version int
	err := tx.QueryRowContext(ctx, "SELECT version FROM resources WHERE kind=? AND id=?", kind, id).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		if expected == 0 {
			return nil
		}
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if version != expected {
		return ErrConflict
	}
	return nil
}
