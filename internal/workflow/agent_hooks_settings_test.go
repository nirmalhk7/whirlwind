package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestAgentReceivesOnlyItsHooksFromGlobalSettings(t *testing.T) {
	e, task, cwd := testEngine(t)
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", "")
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", t.TempDir())
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Policy.AllowedPaths = []string{"**"}
	putFixtureAgent(t, e.Store, "code", map[string]any{"role": "code", "chain": []map[string]string{{"provider": "fake", "model": chosenModelID}}})
	config := json.RawMessage(`{"agent_hooks":{
		"code":{"after":[{"name":"coding-hook","command":["/usr/bin/true"]}]},
		"review":{"after":[{"name":"review-hook","command":["/usr/bin/false"]}]}
	}}`)
	settingsPath := filepath.Join(cwd, "custom-settings.yaml")
	if err := os.WriteFile(settingsPath, []byte("original settings"), 0600); err != nil {
		t.Fatal(err)
	}
	e.Store.Source = &agentHookSettingsSource{path: settingsPath, apply: func(ctx context.Context) error {
		return e.Store.Put(store.WithoutSettingsSource(ctx), "workflow_config", "global", config, 0)
	}}
	var agentInput []byte
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
			agentInput, _ = json.Marshal(input)
			client := provider.NewClient(input, provider.Hooks{Permission: func(context.Context, provider.Permission) (bool, error) {
				return true, nil
			}})
			request := provider.Message{Method: "fs/write_text_file", Params: json.RawMessage(`{"path":"custom-settings.yaml","content":"bad"}`)}
			_, err := client.Handle(ctx, request)
			if err == nil || !strings.Contains(err.Error(), "supervisor settings denied") {
				t.Errorf("host ACP write did not protect supervisor settings: %v", err)
			}
			return provider.Output{Text: "completed"}, nil
		})
	}
	out, err := e.agent(ctx, project, task, provider.Input{Role: "code", CWD: cwd, Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentInput), `"name":"coding-hook"`) || strings.Contains(string(agentInput), `"name":"review-hook"`) {
		t.Fatal("sandbox did not receive only the coding role's settings hooks")
	}
	record, err := e.Store.Get(ctx, "sessions", out.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.Decode[model.Attempt](record)
	if err != nil {
		t.Fatal(err)
	}
	_, snapshot, err := e.Store.Artifact(ctx, attempt.InputArtifact)
	if err != nil || !strings.Contains(string(snapshot), `"name":"coding-hook"`) {
		t.Fatalf("hook commands missing from recorded attempt input: %v", err)
	}
}

type agentHookSettingsSource struct {
	path  string
	once  sync.Once
	apply func(context.Context) error
	err   error
}

func (s *agentHookSettingsSource) SettingsPath() string { return s.path }

func (s *agentHookSettingsSource) Refresh(ctx context.Context) error {
	s.once.Do(func() { s.err = s.apply(ctx) })
	return s.err
}

func (*agentHookSettingsSource) Persist(_ context.Context, _ []store.Change, commit func() error) error {
	return commit()
}
