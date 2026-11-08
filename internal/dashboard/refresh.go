package dashboard

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"whirlwind/internal/control"
)

type inspection struct {
	snapshot control.Snapshot
	err      error
}

// Watch reads state outside the event loop. Redraws consume only the latest
// completed read, so database delays cannot freeze keyboard handling.
func (d *Dashboard) Watch(ctx context.Context, load func(context.Context) (control.Snapshot, error)) <-chan struct{} {
	var latest atomic.Pointer[inspection]
	d.SetBeforeDrawFunc(func(tcell.Screen) bool {
		if result := latest.Swap(nil); result != nil {
			if result.err != nil {
				d.status.SetText("Whirlwind | State unavailable: " + result.err.Error())
			} else {
				d.status.SetText(d.supervisorStatus(result.snapshot))
				d.Present(result.snapshot)
			}
		}
		return false
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			snapshot, err := load(ctx)
			if ctx.Err() != nil {
				d.Stop()
				return
			}
			latest.Store(&inspection{snapshot, err})
			d.QueueEvent(tcell.NewEventResize(0, 0))
			select {
			case <-ctx.Done():
				d.Stop()
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}
