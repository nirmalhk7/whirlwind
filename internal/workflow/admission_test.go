package workflow

import (
	"context"
	"errors"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// Missing finite allowances must never launch an inference.
func TestMissingBudgetBlocksInferenceBeforeAttempt(t *testing.T) {
	h := newRetryHarness(t)
	h.project.Policy.Budget = nil
	h.task.Budget = nil
	r, _ := h.engine.Store.Get(context.Background(), "settings", "global")
	settings := DefaultSettings()
	settings.Admission.Pools = map[string]model.QuotaReserve{"*": {MaxAgeSeconds: 30}}
	if err := h.engine.Store.Put(context.Background(), "settings", "global", settings, r.Version); err != nil {
		t.Fatal(err)
	}
	_, err := h.run(context.Background())
	if !errors.Is(err, store.ErrBudgetExceeded) {
		t.Fatalf("missing budget allowed inference: %v", err)
	}
	attempts, _ := h.engine.Store.List(context.Background(), "sessions")
	if len(attempts) != 0 {
		t.Fatalf("created %d attempts before admission", len(attempts))
	}
}

// Protected reserve must prevent a model turn when fresh native quota is too low.
func TestQuotaReserveBlocksBeforeInferenceAndReleasesSlots(t *testing.T) {
	h := newRetryHarness(t)
	ctx := context.Background()
	r, _ := h.engine.Store.Get(ctx, "providers", "fake")
	p, _ := store.Decode[model.Provider](r)
	used := 85.0
	p.QuotaSnapshot = &model.QuotaSnapshot{Pool: "fake", Source: "metadata", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Windows: []model.QuotaWindow{{LimitID: "main", Window: "primary", UsedPercent: &used}}}
	if err := h.engine.Store.Put(ctx, "providers", p.ID, p, r.Version); err != nil {
		t.Fatal(err)
	}
	sr, _ := h.engine.Store.Get(ctx, "settings", "global")
	settings := DefaultSettings()
	settings.Admission = model.AdmissionSettings{DefaultBudget: &model.BudgetPolicy{MaxAttempts: 4, MaxRuntimeSeconds: 60}, Pools: map[string]model.QuotaReserve{"fake": {MinRemainingPercent: 20, MaxAgeSeconds: 30}}}
	if err := h.engine.Store.Put(ctx, "settings", "global", settings, sr.Version); err != nil {
		t.Fatal(err)
	}
	_, err := h.run(ctx)
	var wait *AdmissionWait
	if !errors.As(err, &wait) {
		t.Fatalf("reserve ignored: %v", err)
	}
	attempts, _ := h.engine.Store.List(ctx, "sessions")
	if len(attempts) != 0 {
		t.Fatalf("reserve consumed model turn: %d", len(attempts))
	}
	if h.engine.active[h.project.ID] != 0 {
		t.Fatal("waiting held execution capacity")
	}
}

// Failed/retried attempts must consume the same task ceiling and retain unknown consumption.
func TestRetryAdmissionCountsFailedAttempts(t *testing.T) {
	h := newRetryHarness(t)
	ctx := context.Background()
	h.project.Policy.Budget = &model.BudgetPolicy{MaxAttempts: 2, MaxRuntimeSeconds: 60, VerificationAttempts: 1, VerificationRuntimeSeconds: 10}
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{}, &provider.TransportError{Cause: errors.New("connection reset by peer")}
		})
	}
	_, err := h.run(ctx)
	if !errors.Is(err, store.ErrBudgetExceeded) {
		t.Fatalf("retry exceeded task allowance: %v", err)
	}
	report, err := h.engine.Store.TaskUsage(ctx, h.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Attempts != 1 || report.UnknownTokenAttempts != 1 {
		t.Fatalf("failed attempt missing: %+v", report)
	}
}

// Replayed native history and repeated cumulative samples must not be billed as new attempts.
func TestReplayUsageDoesNotIncreaseLedger(t *testing.T) {
	h := newRetryHarness(t)
	ctx := context.Background()
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, _ provider.Input, hooks provider.Hooks) (provider.Output, error) {
			hooks.Event("whirlwind/session", map[string]any{"session_id": "s"})
			sample := map[string]any{"tokenUsage": map[string]any{"total": map[string]any{"totalTokens": float64(120), "inputTokens": float64(100), "outputTokens": float64(20), "cachedInputTokens": float64(50)}}}
			hooks.Event("thread/tokenUsage/updated", sample)
			hooks.Event("thread/tokenUsage/updated", sample)
			hooks.Event("whirlwind/replay", sample)
			return provider.Output{Text: "done", SessionID: "s", ResolvedModel: "m"}, nil
		})
	}
	if _, err := h.run(ctx); err != nil {
		t.Fatal(err)
	}
	report, err := h.engine.Store.TaskUsage(ctx, h.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalTokens != 120 || report.CachedTokens != 50 || report.Attempts != 1 {
		t.Fatalf("replay double billed native sample: %+v", report)
	}
	if len(report.Entries) != 1 || len(report.Entries[0].Usage.Raw) == 0 {
		t.Fatal("raw native sample missing")
	}
}

// Discovery now uses its job deadline without requiring a separate budget.
func TestDiscoveryRunsWithoutSeparateAllowance(t *testing.T) {
	h := newRetryHarness(t)
	ctx := context.Background()
	pr, _ := h.engine.Store.Get(ctx, "providers", "fake")
	p, _ := store.Decode[model.Provider](pr)
	p.Capabilities = append(p.Capabilities, "ideate")
	if err := h.engine.Store.Put(ctx, "providers", p.ID, p, pr.Version); err != nil {
		t.Fatal(err)
	}
	sr, _ := h.engine.Store.Get(ctx, "settings", "global")
	settings, _ := store.Decode[Settings](sr)
	settings.Admission.DiscoveryBudget = nil
	if err := h.engine.Store.Put(ctx, "settings", "global", settings, sr.Version); err != nil {
		t.Fatal(err)
	}
	_, err := h.engine.agent(ctx, h.project, h.task, provider.Input{Role: "ideate", CWD: h.cwd, ReadOnly: true, Prompt: "find a candidate"})
	if err != nil {
		t.Fatalf("discovery requires an obsolete allowance: %v", err)
	}
	attempts, _ := h.engine.Store.List(ctx, "sessions")
	if len(attempts) != 1 {
		t.Fatalf("expected one discovery attempt, got %d", len(attempts))
	}
}

// A known prelaunch persistence failure must not strand a shared runner reservation.
func TestPrelaunchFailureReleasesReservationWithoutInventingSpend(t *testing.T) {
	h := newRetryHarness(t)
	ctx := context.Background()
	if _, err := h.engine.Store.DB.Exec(`CREATE TRIGGER reject_attempt BEFORE INSERT ON resources WHEN NEW.kind='sessions' BEGIN SELECT RAISE(ABORT,'fixture prelaunch failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.run(ctx); err == nil {
		t.Fatal("prelaunch failure hidden")
	}
	report, err := h.engine.Store.TaskUsage(ctx, h.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.ActiveAttempts != 0 || report.ReservedRuntimeSeconds != 0 || report.UnknownTokenAttempts != 0 || report.TotalTokens != 0 {
		t.Fatalf("unstarted reservation stranded: %+v", report)
	}
}
