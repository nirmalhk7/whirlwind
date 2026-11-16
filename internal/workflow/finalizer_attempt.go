package workflow

import (
	"context"
	"fmt"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// Heartbeats change the session version without changing its owner. Read that
// version immediately before a guarded write, serializing only the short write
// with this worker's heartbeat. Other owners remain fenced by the transaction.
func (a *agentAttempt) withLiveFinalizerAttempt(ctx context.Context, write func(model.Record, model.Attempt) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	record, err := a.Store.Get(ctx, "sessions", a.id)
	if err != nil {
		return err
	}
	current, err := store.Decode[model.Attempt](record)
	if err != nil {
		return err
	}
	if !sameRuntimeOwner(current, a.state) || !runtimeLeaseActive(current, time.Now().UTC()) {
		return fmt.Errorf("finalizer runtime owner changed or expired: %w", store.ErrConflict)
	}
	return write(record, current)
}
