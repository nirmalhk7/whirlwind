package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestPolicyDenialStopsBeforeQuotaFallback(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "first"}, model.AgentChoice{Provider: "fake", Model: "second"})
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, _ provider.Input, hooks provider.Hooks) (provider.Output, error) {
			calls++
			hooks.Event("whirlwind/quota", map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 100}}})
			return provider.Output{}, errors.New("tool permission denied")
		})
	}
	_, err := h.run(context.Background())
	if err == nil || calls != 1 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
	if strings.Contains(err.Error(), "fallback list") {
		t.Fatalf("policy denial was treated as provider exhaustion: %v", err)
	}
}
