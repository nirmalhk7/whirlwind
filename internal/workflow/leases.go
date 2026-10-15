package workflow

import (
	"context"
	"time"
)

// renewLease keeps a minute-long lease alive, cancelling work if renewal or its optional validity check fails.
func (e *Engine) renewLease(ctx context.Context, cancel context.CancelFunc, id string, fence int64, valid func() bool) {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.Store.Renew(ctx, id, e.owner, fence, time.Minute) != nil || (valid != nil && !valid()) {
					cancel()
					return
				}
			}
		}
	}()
}
