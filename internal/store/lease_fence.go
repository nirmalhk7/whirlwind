package store

import (
	"context"
	"database/sql"
	"time"
)

type leaseFenceKey struct{}
type leaseFence struct {
	key   string
	lease Lease
}

// WithLeaseFence binds subsequent transactional writes to a live ownership
// token. Model calls remain outside SQLite transactions.
func WithLeaseFence(ctx context.Context, key string, lease Lease) context.Context {
	previous, _ := ctx.Value(leaseFenceKey{}).([]leaseFence)
	fences := append(append([]leaseFence{}, previous...), leaseFence{key: key, lease: lease})
	return context.WithValue(ctx, leaseFenceKey{}, fences)
}

func validateLeaseFences(ctx context.Context, tx *sql.Tx) error {
	fences, _ := ctx.Value(leaseFenceKey{}).([]leaseFence)
	for _, fence := range fences {
		var valid bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM leases WHERE id=? AND owner=? AND fence=? AND expires>?)", fence.key, fence.lease.Owner, fence.lease.Fence, time.Now().UnixMilli()).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrLeased
		}
	}
	return nil
}
