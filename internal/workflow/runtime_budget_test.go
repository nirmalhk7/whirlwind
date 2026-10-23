package workflow

import (
	"context"
	"testing"
	"time"
)

// Human wait must not exhaust active inference runtime.
func TestActiveRuntimeBudgetExcludesHumanWait(t *testing.T) {
	ctx, clock := newRuntimeBudget(context.Background(), 80*time.Millisecond)
	defer clock.stop()
	resume := clock.pause()
	time.Sleep(120 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("human wait consumed active runtime")
	}
	resume()
	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("active inference exceeded runtime without cancellation")
	}
	if !clock.exhausted() {
		t.Fatal("runtime cancellation missing budget reason")
	}
}
