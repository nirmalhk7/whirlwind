package workflow

import (
	"context"
	"encoding/json"
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

func TestConfiguredAgentUsesOrderedProviderModelsAndPreservesWork(t *testing.T) {
	e, task, cwd := testEngine(t)
	cwd, _ = filepath.EvalSymlinks(cwd)
	ctx := context.Background()
	for _, id := range []string{"primary", "fallback", "outside"} {
		p := model.Provider{ID: id, Kind: "codex", Available: true, Quota: "unknown", Capabilities: []string{"code"}}
		if err := e.Store.Put(ctx, "providers", id, p, 0); err != nil {
			t.Fatal(err)
		}
	}
	chain := map[string]any{"role": "code", "chain": []map[string]string{{"provider": "primary", "model": "model-one"}, {"provider": "fallback", "model": "model-two"}}}
	if err := e.Store.Put(ctx, "agents", "code", chain, 0); err != nil {
		t.Fatal(err)
	}
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
	for _, failure := range []string{"quota exhausted", "invalid model"} {
		t.Run(failure, func(t *testing.T) {
			e, task, cwd := testEngine(t)
			ctx := context.Background()
			if err := e.Store.Put(ctx, "agents", "code", map[string]any{"role": "code", "chain": []map[string]string{{"provider": "fake", "model": "chosen-model"}}}, 0); err != nil {
				t.Fatal(err)
			}
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

func TestFallbackRetainsEachChoicesThinkingAndRunOutcome(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, _ := e.Project(ctx, task.ProjectID)
	for _, id := range []string{"primary", "fallback"} {
		_ = e.Store.Put(ctx, "providers", id, model.Provider{ID: id, Name: id, Kind: "codex", Available: true, Quota: "unknown", Capabilities: []string{"code"}}, 0)
	}
	chain := model.AgentConfig{Role: "code", Chain: []model.AgentChoice{
		{Provider: "primary", Model: "m1", Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}},
		{Provider: "fallback", Model: "m2", Thinking: &model.ThinkingChoice{ConfigID: "reasoning", Value: "low"}},
	}}
	_ = e.Store.Put(ctx, "agents", "code", chain, 0)
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

func thinkingFallbackOutput(t *testing.T, source model.Provider, input provider.Input, chain model.AgentConfig) (provider.Output, error) {
	t.Helper()
	if source.Kind != "acp" {
		t.Errorf("thinking used native transport: %s", source.Kind)
	}
	want := chain.Chain[0].Thinking
	if source.ID == "fallback" {
		want = chain.Chain[1].Thinking
	}
	if input.Thinking == nil || *input.Thinking != *want {
		t.Errorf("thinking crossed fallback: %+v", input.Thinking)
	}
	if source.ID == "primary" {
		return provider.Output{}, errors.New("quota exhausted")
	}
	return provider.Output{Text: "done"}, nil
}

func assertThinkingFallbackAttempts(t *testing.T, e *Engine, ctx context.Context) {
	t.Helper()
	records, _ := e.Store.List(ctx, "sessions")
	if len(records) != 2 {
		t.Fatalf("attempts collapsed: %d", len(records))
	}
	for _, record := range records {
		var state struct {
			Provider string                `json:"provider"`
			Success  bool                  `json:"success"`
			Error    string                `json:"error"`
			Thinking *model.ThinkingChoice `json:"thinking"`
		}
		_ = json.Unmarshal(record.Data, &state)
		if state.Provider == "primary" && (state.Success || state.Error == "" || state.Thinking.Value != "high") {
			t.Fatalf("failed attempt lost: %+v", state)
		}
		if state.Provider == "fallback" && (!state.Success || state.Thinking.Value != "low") {
			t.Fatalf("fallback attempt lost: %+v", state)
		}
	}
}
