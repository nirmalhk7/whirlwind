package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Claim claims an absent or expired lease and increments its fencing token; an unexpired lease returns ErrLeased.
func (s *Store) Claim(ctx context.Context, id, owner string, ttl time.Duration) (Lease, error) {
	var l Lease
	e := s.Transaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		var old Lease
		e := tx.QueryRowContext(ctx, "SELECT owner,fence,expires FROM leases WHERE id=?", id).Scan(&old.Owner, &old.Fence, &old.ExpiresAt)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if old.ExpiresAt > now {
			return ErrLeased
		}
		l = Lease{owner, old.Fence + 1, now + ttl.Milliseconds()}
		_, e = tx.ExecContext(ctx, "INSERT INTO leases VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET owner=excluded.owner,fence=excluded.fence,expires=excluded.expires", id, l.Owner, l.Fence, l.ExpiresAt)
		return e
	})
	return l, e
}

// Renew extends a live lease only when its owner and fencing token still match.
func (s *Store) Renew(ctx context.Context, id, owner string, fence int64, ttl time.Duration) error {
	res, e := s.DB.ExecContext(ctx, "UPDATE leases SET expires=? WHERE id=? AND owner=? AND fence=? AND expires>?", time.Now().Add(ttl).UnixMilli(), id, owner, fence, time.Now().UnixMilli())
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrLeased
	}
	return nil
}

// Release expires a lease only for its current owner and fencing token.
func (s *Store) Release(ctx context.Context, id, owner string, fence int64) error {
	res, e := s.DB.ExecContext(ctx, "UPDATE leases SET expires=0 WHERE id=? AND owner=? AND fence=?", id, owner, fence)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrLeased
	}
	return nil
}
