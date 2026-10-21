package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestContextRotationStartsFreshAfterRestart(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
			calls++
			if calls == 1 {
				h.Event("whirlwind/context", model.ContextUsage{Used: 90, Size: 100, Source: "acp"})
				return provider.Output{Text: "done", SessionID: "full-session"}, nil
			}
			if in.SessionID != "" || !strings.Contains(in.Prompt, "Recovery checkpoint") {
				t.Fatalf("full session resumed or checkpoint missing: %+v", in)
			}
			return provider.Output{Text: "done", SessionID: "fresh-session"}, nil
		})
	}
	input := provider.Input{Role: "code", CWD: cwd, Prompt: "continue task"}
	if _, err := e.agent(ctx, p, task, input); err != nil {
		t.Fatal(err)
	}
	if _, err := e.agent(ctx, p, task, input); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedFailedActionsRecoverWithBoundedAttempts(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, _ provider.Input, h provider.Hooks) (provider.Output, error) {
			calls++
			for _, id := range []string{"a", "b", "c"} {
				h.Event("whirlwind/tool", model.ToolActivity{ID: id, Kind: "execute", Status: "failed", InputHash: "same", OutputHash: "same"})
			}
			return provider.Output{Text: "stuck", SessionID: "spinning"}, nil
		})
	}
	_, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "fix retry handling"})
	if err == nil || calls != 3 {
		t.Fatalf("recovery cap: err=%v calls=%d", err, calls)
	}
	saved, _ := e.Store.Get(ctx, "tasks", task.ID)
	var got model.Task
	_ = json.Unmarshal(saved.Data, &got)
	if got.Stage != "paused" {
		t.Fatalf("exhausted recovery did not pause: %s", got.Stage)
	}
}
