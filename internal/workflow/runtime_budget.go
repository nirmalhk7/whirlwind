package workflow

import (
	"context"
	"sync"
	"time"
)

// runtimeBudget stops its clock while the transport awaits a human decision.
type runtimeBudget struct {
	mu               sync.Mutex
	timer            *time.Timer
	cancel           context.CancelFunc
	remaining, total time.Duration
	since            time.Time
	waiting          int
	generation       uint64
	expired, stopped bool
}

func newRuntimeBudget(parent context.Context, allowance time.Duration) (context.Context, *runtimeBudget) {
	ctx, cancel := context.WithCancel(parent)
	b := &runtimeBudget{cancel: cancel, remaining: allowance, total: allowance, since: time.Now()}
	b.arm()
	return ctx, b
}
func (b *runtimeBudget) arm() {
	b.generation++
	generation := b.generation
	b.timer = time.AfterFunc(b.remaining, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.stopped || b.waiting > 0 || b.generation != generation {
			return
		}
		b.remaining = 0
		b.expired = true
		b.cancel()
	})
}
func (b *runtimeBudget) pause() func() {
	b.mu.Lock()
	if !b.stopped && !b.expired && b.waiting == 0 {
		b.timer.Stop()
		b.generation++
		b.remaining -= time.Since(b.since)
		if b.remaining < 0 {
			b.remaining = 0
		}
	}
	b.waiting++
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.waiting--
			if b.waiting == 0 && !b.stopped && !b.expired {
				b.since = time.Now()
				b.arm()
			}
		})
	}
}
func (b *runtimeBudget) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return
	}
	b.timer.Stop()
	if b.waiting == 0 && !b.expired {
		b.remaining -= time.Since(b.since)
		if b.remaining < 0 {
			b.remaining = 0
		}
	}
	b.stopped = true
	b.cancel()
}
func (b *runtimeBudget) elapsed() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.remaining
	if !b.stopped && b.waiting == 0 && !b.expired {
		remaining -= time.Since(b.since)
	}
	if remaining < 0 {
		remaining = 0
	}
	return b.total - remaining
}
func (b *runtimeBudget) exhausted() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.expired }
