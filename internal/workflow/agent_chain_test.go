package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const (
	quotaExhaustedFailure = "quota exhausted"
	chosenModelID         = "chosen-model"
)

func TestConfiguredAgentUsesOrderedProviderModelsAndPreservesWork(t *testing.T) {
	e, task, cwd := testEngine(t)
	cwd, _ = filepath.EvalSymlinks(cwd)
	ctx := context.Background()
	for _, id := range []string{"primary", "fallback", "outside"} {
		p := model.Provider{ID: id, Kind: "acp", Available: true, Quota: "unknown", Capabilities: []string{"code"}}
		if err := e.Store.Put(ctx, "providers", id, p, 0); err != nil {
			t.Fatal(err)
		}
	}
	chain := map[string]any{"role": "code", "chain": []map[string]string{{"provider": "primary", "model": "model-one"}, {"provider": "fallback", "model": "model-two"}}}
	putFixtureAgent(t, e.Store, "code", chain)
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Policy.Providers = []string{"outside", "fallback", "primary"}
	var calls []string
	e.Adapter = func(p model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			return orderedFallbackOutput(t, p, in, cwd, &calls)
		})
	}
	out, err := e.agent(ctx, project, task, provider.Input{Role: "code", CWD: cwd, Prompt: "finish"})
	if err != nil || out.Text != "completed" || !reflect.DeepEqual(calls, []string{"primary:model-one", "fallback:model-two"}) {
		t.Fatalf("ordered chain lost: %v %+v %v", calls, out, err)
	}
	record, err := e.Store.Get(ctx, "providers", "primary")
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Decode[model.Provider](record)
	if err != nil || p.Quota != "exhausted" {
		t.Fatalf("quota not retained: %+v %v", p, err)
	}
}

func TestConfiguredAgentDoesNotTryUnlistedProviderOrRetryOrdinaryErrors(t *testing.T) {
	for _, failure := range []string{quotaExhaustedFailure, "invalid model"} {
		t.Run(failure, func(t *testing.T) {
			e, task, cwd := testEngine(t)
			ctx := context.Background()
			putFixtureAgent(t, e.Store, "code", map[string]any{"role": "code", "chain": []map[string]string{{"provider": "fake", "model": chosenModelID}}})
			var calls int
			e.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
					calls++
					return provider.Output{}, errors.New(failure)
				})
			}
			p, err := e.Project(ctx, task.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "work"})
			if err == nil || calls != 1 {
				t.Fatalf("unexpected attempts: %d %v", calls, err)
			}
			if failure == "invalid model" && !strings.Contains(err.Error(), failure) {
				t.Fatalf("ordinary error hidden: %v", err)
			}
		})
	}
}

func TestCanonicalRoleWithoutQueueDoesNotRouteToAvailableProvider(t *testing.T) {
	e, task, cwd := testEngine(t)
	putFixtureAgent(t, e.Store, "code", model.AgentConfig{Role: "code"})
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			return provider.Output{Text: "unexpected"}, nil
		})
	}
	_, err = e.agent(context.Background(), project, task, provider.Input{Role: "code", CWD: cwd, Prompt: "finish"})
	if err == nil || !strings.Contains(err.Error(), "queue") || calls != 0 {
		t.Fatalf("missing role queue fell through: calls=%d err=%v", calls, err)
	}
}

func TestFallbackRetainsEachChoicesThinkingAndRunOutcome(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, _ := e.Project(ctx, task.ProjectID)
	for _, id := range []string{"primary", "fallback"} {
		_ = e.Store.Put(ctx, "providers", id, model.Provider{ID: id, Name: id, Kind: "acp", Available: true, Quota: "unknown", Capabilities: []string{"code"}}, 0)
	}
	chain := model.AgentConfig{Role: "code", Chain: []model.AgentChoice{
		{Provider: "primary", Model: "m1", Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}},
		{Provider: "fallback", Model: "m2", Thinking: &model.ThinkingChoice{ConfigID: "reasoning", Value: "low"}},
	}}
	putFixtureAgent(t, e.Store, "code", chain)
	e.Adapter = func(source model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
			return thinkingFallbackOutput(t, source, input, chain)
		})
	}
	if _, err := e.agent(ctx, project, task, provider.Input{Role: "code", CWD: cwd, Prompt: "work"}); err != nil {
		t.Fatal(err)
	}
	assertThinkingFallbackAttempts(t, e, ctx)
}

func orderedFallbackOutput(t *testing.T, p model.Provider, in provider.Input, cwd string, calls *[]string) (provider.Output, error) {
	t.Helper()
	*calls = append(*calls, p.ID+":"+in.Model)
	if in.CWD != cwd {
		t.Errorf("worktree changed: %s", in.CWD)
	}
	if p.ID == "primary" {
		if err := os.WriteFile(filepath.Join(cwd, "partial.txt"), []byte("preserve me"), 0600); err != nil {
			t.Fatal(err)
		}
		return provider.Output{SessionID: "exhausted-session"}, errors.New("usageLimitExceeded")
	}
	if p.ID != "fallback" || in.SessionID != "" || !strings.Contains(in.Prompt, "preserve completed work") {
		t.Errorf("bad fallback: %s %+v", p.ID, in)
	}
	if contents, err := os.ReadFile(filepath.Join(cwd, "partial.txt")); err != nil || string(contents) != "preserve me" {
		t.Errorf("partial work lost: %v", err)
	}
	return provider.Output{Text: "completed", SessionID: "fallback-session"}, nil
}
