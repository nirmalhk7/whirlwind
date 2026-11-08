package workflow

import (
	"context"
	"encoding/json"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"

	"whirlwind/internal/store"
)

func TestResumeSessionMatchesSelectedModel(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	for _, item := range []struct{ model, session string }{{"model-one", "session-one"}, {"model-two", "session-two"}} {
		if err := e.Store.Put(ctx, "sessions", store.ID(), map[string]any{"task": task.ID, "role": "code", "provider": "fake", "cwd": cwd, "plan_version": 1, "model": item.model, "session_id": item.session}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := e.resumeSession(ctx, task.ID, "code", "fake", cwd, 1, "model-one"); err != nil || got != "session-one" {
		t.Fatalf("wrong model resumed: %s", got)
	}
	if got, err := e.resumeSession(ctx, task.ID, "code", "fake", cwd, 1, "unseen-model"); err != nil || got != "" {
		t.Fatalf("new model reused session: %s", got)
	}
}

func TestRunRecordsAttemptOutcomeAndIdentity(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, _ := e.Project(ctx, task.ProjectID)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{Text: "done", SessionID: "finished"}, nil
		})
	}
	if _, err := e.agent(ctx, project, task, provider.Input{Role: "code", CWD: cwd, Prompt: "work"}); err != nil {
		t.Fatal(err)
	}
	records, _ := e.Store.List(ctx, "sessions")
	if len(records) != 1 {
		t.Fatalf("attempts=%d", len(records))
	}
	var state map[string]any
	_ = json.Unmarshal(records[0].Data, &state)
	for _, key := range []string{"project_id", "provider_name", "transport", "started_at", "finished_at", "elapsed_ms", "success"} {
		if _, ok := state[key]; !ok {
			t.Errorf("missing attempt %s", key)
		}
	}
	if state["success"] != true {
		t.Errorf("outcome=%v", state["success"])
	}
}

func TestResumeAttemptMatchesTransportAndThinking(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, _ := e.Project(ctx, task.ProjectID)
	source := model.Provider{ID: "native", Kind: "acp"}
	_ = e.Store.Put(ctx, "providers", source.ID, source, 0)
	for _, item := range []struct{ transport, value, session string }{{"acp", "high", "acp-high"}, {"acp", "low", "acp-low"}, {"codex", "", "native-default"}} {
		state := map[string]any{"elapsed_ms": 0, "task": task.ID, "role": "code", "provider": source.ID, "cwd": cwd, "plan_version": task.PlanVersion, "model": "m", "session_id": item.session, "transport": item.transport}
		if item.value != "" {
			state["thinking"] = map[string]string{"config_id": "effort", "value": item.value}
		}
		profileRun := &agentRun{Engine: e, ctx: ctx, project: project, task: task, input: provider.Input{Role: "code", CWD: cwd, Model: "m"}}
		if item.value != "" {
			profileRun.input.Thinking = &model.ThinkingChoice{ConfigID: "effort", Value: item.value}
		}
		if err := profileRun.prepareExecutionProfile(source); err != nil {
			t.Fatal(err)
		}
		state["execution_profile"] = profileRun.input.ExecutionProfile
		_ = e.Store.Put(ctx, "sessions", store.ID(), state, 0)
	}
	a := &agentRun{Engine: e, ctx: ctx, project: project, task: task, input: provider.Input{Role: "code", CWD: cwd, Model: "m", Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}}}
	if _, err := a.newAttempt(source); err != nil {
		t.Fatal(err)
	}
	if a.input.SessionID != "acp-high" {
		t.Fatalf("wrong thinking/transport resumed: %s", a.input.SessionID)
	}
}
