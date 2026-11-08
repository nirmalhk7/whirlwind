package workflow

import (
	"context"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// Missing or restrictive legacy budgets must not gate preparation jobs or live
// usage events; usage must still be recorded for every completed attempt.
func TestPreparationUsesOnlyDeadline(t *testing.T) {
	for _, role := range []string{"ideate", "plan"} {
		for _, configured := range []bool{false, true} {
			name := role + "/missing budgets"
			if configured {
				name = role + "/restrictive budgets"
			}
			t.Run(name, func(t *testing.T) { assertPreparationWithoutBudget(t, role, configured) })
		}
	}
}

func assertPreparationWithoutBudget(t *testing.T, role string, configured bool) {
	t.Helper()
	h := preparationHarness(t, configured)
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
			assertFiveMinuteDeadline(t, ctx, time.Now())
			calls++
			tokens := int64(1000)
			cost := 100.0
			usage := &model.Usage{Scope: "attempt", TotalTokens: &tokens, Cost: &cost, Source: "fixture"}
			hooks.Event("whirlwind/quota", map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 99.0}}})
			hooks.Event("agent/event", map[string]any{"usage": usage})
			output := provider.Output{Text: "done"}
			if role == "plan" {
				var err error
				output, err = (fakeAgent{9}).Run(ctx, in, hooks)
				if err != nil {
					return output, err
				}
			}
			output.Usage = usage
			return output, nil
		})
	}
	for range 2 {
		_, err := h.engine.agent(context.Background(), h.project, h.task, provider.Input{Role: role, CWD: h.cwd, Prompt: "prepare", ReadOnly: true})
		if err != nil {
			t.Fatalf("preparation blocked by budget: %v", err)
		}
	}
	if calls != 2 {
		t.Fatalf("preparation did not run: %d", calls)
	}
	report, err := h.engine.Store.TaskUsage(context.Background(), h.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Attempts != 2 || report.TotalTokens != 2000 || report.Cost != 200 {
		t.Fatalf("usage lost: %+v", report)
	}
	reservations, err := h.engine.Store.List(context.Background(), "budget_reservations")
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 0 {
		t.Fatal("preparation created budget reservations")
	}
}

func preparationHarness(t *testing.T, configured bool) retryHarness {
	t.Helper()
	h := newRetryHarness(t)
	ctx := context.Background()
	pr, _ := h.engine.Store.Get(ctx, "providers", "fake")
	p, _ := store.Decode[model.Provider](pr)
	p.Capabilities = append(p.Capabilities, "ideate")
	used := 99.0
	p.QuotaSnapshot = &model.QuotaSnapshot{Pool: "fake", Source: "fixture", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Windows: []model.QuotaWindow{{UsedPercent: &used, Window: "primary"}}}
	if err := h.engine.Store.Put(ctx, "providers", p.ID, p, pr.Version); err != nil {
		t.Fatal(err)
	}
	sr, _ := h.engine.Store.Get(ctx, "settings", "global")
	settings, _ := store.Decode[Settings](sr)
	settings.Admission = model.AdmissionSettings{}
	tiny := &model.BudgetPolicy{MaxAttempts: 1, MaxRuntimeSeconds: 1, MaxTokens: 1, AttemptTokens: 1, MaxCost: 1, AttemptCost: 1}
	h.project.Policy.Budget = tiny
	h.task.Budget = tiny
	if configured {
		settings.Admission = model.AdmissionSettings{DiscoveryBudget: tiny, DefaultBudget: tiny, Pools: map[string]model.QuotaReserve{"fake": {MinRemainingPercent: 99, MaxAgeSeconds: 60, MaxConcurrent: 1}}}
	}
	if err := h.engine.Store.Put(ctx, "settings", "global", settings, sr.Version); err != nil {
		t.Fatal(err)
	}
	return h
}

// Preparation always gets its five-minute ceiling, independent of legacy
// project runtime allowances. Explicit cancellation still propagates.
func TestPreparationIgnoresLegacyRuntimeLimit(t *testing.T) {
	for _, role := range []string{"plan", "ideate"} {
		t.Run(role, func(t *testing.T) {
			h := preparationHarness(t, false)
			h.project.Policy.RunTimeoutSeconds = 1
			h.engine.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
					assertFiveMinuteDeadline(t, ctx, time.Now())
					if role == "plan" {
						return (fakeAgent{9}).Run(ctx, in, hooks)
					}
					return provider.Output{Text: "done"}, nil
				})
			}
			if _, err := h.engine.agent(context.Background(), h.project, h.task, provider.Input{Role: role, CWD: h.cwd, Prompt: "prepare", ReadOnly: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
