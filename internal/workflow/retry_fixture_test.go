package workflow

import (
	"context"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

const retryFailureFormat = "calls=%d error=%v"

type retryHarness struct {
	engine  *Engine
	task    model.Task
	project model.Project
	cwd     string
}

func newRetryHarness(t *testing.T, chain ...model.AgentChoice) retryHarness {
	t.Helper()
	e, task, cwd := testEngine(t)
	cwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	e.retryWait = func(context.Context, time.Duration) error { return nil }
	p, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if chain != nil {
		if err := e.Store.Put(context.Background(), "agents", "code", model.AgentConfig{Role: "code", Chain: chain}, 0); err != nil {
			t.Fatal(err)
		}
	}
	return retryHarness{engine: e, task: task, project: p, cwd: cwd}
}

func (h retryHarness) run(ctx context.Context) (provider.Output, error) {
	return h.engine.agent(ctx, h.project, h.task, provider.Input{Role: "code", CWD: h.cwd, Prompt: "finish"})
}

func (h retryHarness) executionProfile(t *testing.T) string {
	t.Helper()
	profile, err := execution.ForProvider(h.cwd, false, h.project.PolicyHash, h.project.Policy.Sandbox, nil)
	if err != nil {
		t.Fatal(err)
	}
	profile.AllowedPaths = append([]string{}, h.project.Policy.AllowedPaths...)
	return profile.Digest()
}

func fixtureRuntime(ms int64) *int64 { return &ms }
