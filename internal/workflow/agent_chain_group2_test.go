package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

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
		return provider.Output{}, errors.New(quotaExhaustedFailure)
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

func TestConfiguredAgentExhaustionIncludesProviderCause(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, _ := e.Project(ctx, task.ProjectID)
	source := model.Provider{ID: "blocked", Kind: "codex", Available: true, Quota: "exhausted"}
	source.Capabilities = []string{"code"}
	if err := e.Store.Put(ctx, "providers", source.ID, source, 0); err != nil {
		t.Fatal(err)
	}
	chain := model.AgentConfig{Role: "code", Chain: []model.AgentChoice{{Provider: "blocked", Model: chosenModelID}}}
	if err := e.Store.Put(ctx, "agents", "code", chain, 0); err != nil {
		t.Fatal(err)
	}
	_, err := e.agent(ctx, project, task, provider.Input{Role: "code", CWD: cwd, Prompt: "work"})
	if err == nil || !strings.Contains(err.Error(), "blocked/"+chosenModelID) || !strings.Contains(err.Error(), quotaExhaustedFailure) {
		t.Fatalf("fallback exhaustion lost actionable cause: %v", err)
	}
}

func TestConfiguredAgentExhaustionPreservesExecutionFailure(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, _ := e.Project(ctx, task.ProjectID)
	chain := model.AgentConfig{Role: "code", Chain: []model.AgentChoice{{Provider: "fake", Model: chosenModelID}}}
	if err := e.Store.Put(ctx, "agents", "code", chain, 0); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("usageLimitExceeded: weekly limit reached")
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{}, failure
		})
	}
	_, err := e.agent(ctx, project, task, provider.Input{Role: "code", CWD: cwd, Prompt: "work"})
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "fake/"+chosenModelID) {
		t.Fatalf("fallback lost execution error: %v", err)
	}
}
