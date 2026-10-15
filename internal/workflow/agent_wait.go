package workflow

import (
	"context"
	"time"
	"whirlwind/internal/store"
)

// awaitResource preserves the one-second polling cadence and stops on cancellation or decode failure.
func awaitResource[T any](ctx context.Context, s *store.Store, kind, id string, ready func(T) bool) (T, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var zero, value T
	for {
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-ticker.C:
			record, err := s.Get(ctx, kind, id)
			if err != nil {
				return zero, err
			}
			value, err = store.Decode[T](record)
			if err != nil {
				return zero, err
			}
			if ready(value) {
				return value, nil
			}
		}
	}
}
