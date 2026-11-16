package workflow

import (
	"context"
	"errors"
	"time"
	"whirlwind/internal/store"
)

var ErrHumanDecisionPending = errors.New("human decision pending")

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

// A transport may expose a hook error as a tool response and keep sampling.
// Persisted human intent therefore revokes execution independently of whether
// the peer propagates the hook error to its top-level Run call.
func (a *agentAttempt) endForHumanDecision() error {
	a.mu.Lock()
	a.eventErr = errors.Join(a.eventErr, ErrHumanDecisionPending)
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return ErrHumanDecisionPending
}
