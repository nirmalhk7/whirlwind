package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

// A provider-requested long delay must survive restart without holding a runner.
func TestLongRetryDelayBecomesDurableWait(t *testing.T) {
	h := newRetryHarness(t)
	h.engine.retryWait = nil
	ctx := context.Background()
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{}, &provider.RPCError{Code: 429, Message: "rate limited", Data: json.RawMessage(`{"retryAfter":60}`)}
		})
	}
	_, err := h.run(ctx)
	var wait *AdmissionWait
	if !errors.As(err, &wait) || wait.Kind != "retry" || time.Until(wait.NextCheck) < 59*time.Second {
		t.Fatalf("long delay not durable: %v", err)
	}
	if h.engine.active[h.project.ID] != 0 {
		t.Fatal("retry cooldown held capacity")
	}
	reloaded := New(h.engine.Store, h.engine.GitHub, h.engine.Root)
	run := &agentRun{Engine: reloaded, ctx: ctx, input: provider.Input{Role: "code"}}
	if _, err = run.quotaAdmission(model.Provider{ID: "fake", Available: true}); !errors.As(err, &wait) {
		t.Fatalf("restart forgot cooldown: %v", err)
	}
}
