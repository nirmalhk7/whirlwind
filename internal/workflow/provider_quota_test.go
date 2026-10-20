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

type quotaReaderAdapter struct {
	state, reset string
	err          error
}

func (q quotaReaderAdapter) Run(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
	panic("quota refresh must not run inference")
}
func (q quotaReaderAdapter) ReadQuota(ctx context.Context, cwd string) (string, string, error) {
	return q.state, q.reset, q.err
}

func TestLoadProvidersRechecksBlockedCodexQuota(t *testing.T) {
	for _, tc := range []struct {
		name, savedReset, state, reset string
		err                            error
		want                           string
	}{
		{name: "legacy exhaustion without reset", state: "available", want: "available"},
		{name: "early account reset", savedReset: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339), state: "available", want: "available"},
		{name: "still exhausted", state: "exhausted", reset: "2030-01-01T00:00:00Z", want: "exhausted"},
		{name: "offline", err: errors.New("network unavailable"), want: "exhausted"},
		{name: "missing quota", state: "unknown", want: "exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBlockedQuotaRefresh(t, tc) })
	}
}

func assertBlockedQuotaRefresh(t *testing.T, tc struct {
	name, savedReset, state, reset string
	err                            error
	want                           string
}) {
	t.Helper()
	e, _, cwd := testEngine(t)
	ctx := context.Background()
	source := model.Provider{ID: "codex", Kind: "codex", Available: true, Quota: "exhausted", ResetAt: tc.savedReset, Capabilities: []string{"code"}}
	if err := e.Store.Put(ctx, "providers", source.ID, source, 0); err != nil {
		t.Fatal(err)
	}
	e.Adapter = func(model.Provider) provider.Adapter { return quotaReaderAdapter{tc.state, tc.reset, tc.err} }
	run := &agentRun{Engine: e, ctx: ctx, input: provider.Input{CWD: cwd}}
	if err := run.loadProviders(); err != nil {
		t.Fatal(err)
	}
	assertStoredQuota(t, e.Store, ctx, source.ID, tc.want)
	assertLoadedQuota(t, run, source.ID, tc.state, tc.reset, tc.want)
}

func assertStoredQuota(t *testing.T, db *store.Store, ctx context.Context, id, want string) {
	t.Helper()
	record, err := db.Get(ctx, "providers", id)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[model.Provider](record)
	if err != nil || saved.Quota != want {
		t.Fatalf("saved quota=%+v err=%v", saved, err)
	}
}

func assertLoadedQuota(t *testing.T, run *agentRun, id, state, reset, want string) {
	t.Helper()
	loaded := run.configuredProvider(id)
	if loaded.Quota != want {
		t.Fatalf("routing retained stale state: %+v", loaded)
	}
	if state == "available" && loaded.ResetAt != "" {
		t.Fatalf("recovered quota kept stale reset: %+v", loaded)
	}
	if state == "exhausted" && loaded.ResetAt != reset {
		t.Fatalf("fresh reset lost: %+v", loaded)
	}
}

type recoveredQuotaAdapter struct{ quotaReaderAdapter }

func (q recoveredQuotaAdapter) Run(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
	return provider.Output{Text: "recovered"}, nil
}

func TestConfiguredAgentRunsAfterQuotaRecovers(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	source := model.Provider{ID: "codex", Kind: "codex", Available: true, Quota: "exhausted", Capabilities: []string{"code"}}
	if err := e.Store.Put(ctx, "providers", source.ID, source, 0); err != nil {
		t.Fatal(err)
	}
	chain := model.AgentConfig{Role: "code", Chain: []model.AgentChoice{{Provider: "codex", Model: "gpt-6-luna", Thinking: &model.ThinkingChoice{ConfigID: "reasoning_effort", Value: "high"}}}}
	if err := e.Store.Put(ctx, "agents", "code", chain, 0); err != nil {
		t.Fatal(err)
	}
	e.Adapter = func(model.Provider) provider.Adapter {
		return recoveredQuotaAdapter{quotaReaderAdapter{state: "available"}}
	}
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.agent(ctx, project, task, provider.Input{CWD: cwd, Role: "code", Prompt: "work"})
	if err != nil || out.Text != "recovered" {
		t.Fatalf("configured agent remains blocked: %+v %v", out, err)
	}
}
