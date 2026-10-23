package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const stoppedRunDay = "2026-10-19"

func TestStopAllCancelsTaskAndProviderRunnersAndAllowsNewWork(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{}, 2)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, _ provider.Input, _ provider.Hooks) (provider.Output, error) {
			started <- struct{}{}
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	done := make(chan error, 2)
	go func() { done <- e.Step(ctx, task.ID) }()
	go func() { _, err := e.TestProviderConnection(ctx, model.Provider{ID: "chosen"}); done <- err }()
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("runners did not start")
		}
	}
	e.StopAll()
	for range 2 {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("not cancelled: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("runners did not stop")
		}
	}
	e.Adapter = func(model.Provider) provider.Adapter { return fakeAgent{score: 9} }
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatalf("new work blocked: %v", err)
	}
}

func TestStoppedFlagBlocksRunnersUntilCleared(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	initial, _ := e.Store.Get(ctx, "tasks", task.ID)
	settings := DefaultSettings()
	settings.Stopped = true
	record, err := e.Store.Get(ctx, "settings", "global")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if err == nil {
		settings, _ = store.Decode[Settings](record)
		settings.Stopped = true
	}
	if err := e.Store.Put(ctx, "settings", "global", settings, record.Version); err != nil {
		t.Fatal(err)
	}
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []struct {
		name string
		call func() error
	}{
		{"task", func() error { return e.Step(ctx, task.ID) }},
		{"ideation", func() error { return e.IdeateWhenAvailable(ctx, project, stoppedRunDay) }},
		{"provider", func() error { _, err := e.TestProviderConnection(ctx, model.Provider{ID: "fake"}); return err }},
		{"capacity", func() error { return e.acquire(ctx, project) }},
	} {
		t.Run(run.name, func(t *testing.T) {
			if err := run.call(); err == nil {
				t.Fatal("runner started while stopped")
			}
		})
	}
	e.Daily(ctx)
	sources, err := e.Store.List(ctx, "source_cache")
	if err != nil || len(sources) != 0 {
		t.Fatalf("stopped daily ideation collected sources: %d %v", len(sources), err)
	}
	e.dispatch(ctx)
	e.background.Wait()
	current, _ := e.Store.Get(ctx, "tasks", task.ID)
	if current.Version != initial.Version {
		t.Fatalf("stopped dispatch advanced task: %+v", current)
	}
	record, _ = e.Store.Get(ctx, "settings", "global")
	settings.Stopped = false
	if err := e.Store.Put(ctx, "settings", "global", settings, record.Version); err != nil {
		t.Fatal(err)
	}
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatalf("cleared flag still blocks: %v", err)
	}
}

// Uses the real process adapter: the probe records execution before any protocol
// handshake. The enabled control proves that absence of the marker means no spawn.
func TestStoppedFlagNeverSpawnsAgentProcess(t *testing.T) {
	e, task, path := testEngine(t)
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", filepath.Join(root, "processes"))
	marker := filepath.Join(root, "agent-starts.log")
	script := filepath.Join(root, "probe-agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'started\\n' >> \"$HOME/agent-starts.log\"\nexit 73\n"), 0700); err != nil {
		t.Fatal(err)
	}
	p := model.Provider{ID: "probe", Name: "Probe", Kind: "acp", Command: []string{"sh", script, marker}, Capabilities: []string{"ideate", "plan", "code", "review"}, Available: true, Quota: "unknown"}
	e.Adapter = func(p model.Provider) provider.Adapter { return provider.ProcessAdapter{Provider: p} }
	if err := e.Store.Put(ctx, "providers", p.ID, p, 0); err != nil {
		t.Fatal(err)
	}
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	setRunnerStopped(t, e, true)
	assertStoppedRunnerCalls(t, stoppedRunnerFixture{e, project, task, p, path, ctx})
	assertSchedulerDoesNotStartAgent(t, e, ctx, marker, p)
}

func setRunnerStopped(t *testing.T, e *Engine, stopped bool) {
	t.Helper()
	ctx := context.Background()
	record, err := e.Store.Get(ctx, "settings", "global")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	settings := DefaultSettings()
	if err == nil {
		settings, err = store.Decode[Settings](record)
		if err != nil {
			t.Fatal(err)
		}
	}
	settings.Stopped = stopped
	if err := e.Store.Put(ctx, "settings", "global", settings, record.Version); err != nil {
		t.Fatal(err)
	}
}

type stoppedRunnerFixture struct {
	engine   *Engine
	project  model.Project
	task     model.Task
	provider model.Provider
	path     string
	ctx      context.Context
}

func assertStoppedRunnerCalls(t *testing.T, fixture stoppedRunnerFixture) {
	e, project, task, p, ctx := fixture.engine, fixture.project, fixture.task, fixture.provider, fixture.ctx
	t.Helper()
	calls := []struct {
		name string
		run  func() error
	}{
		{"task step", func() error { return e.Step(ctx, task.ID) }},
		{"manual ideation", func() error { return e.IdeateWhenAvailable(ctx, project, stoppedRunDay) }},
		{"direct ideation", func() error { return e.Ideate(ctx, project, stoppedRunDay) }},
		{"provider connectivity", func() error { return stoppedProviderConnection(e, ctx, p) }},
		{"model discovery", func() error { return stoppedProviderModels(e, ctx, p) }},
	}
	for _, role := range []string{"ideate", "plan", "code", "review"} {
		calls = append(calls, struct {
			name string
			run  func() error
		}{role + " agent", stoppedAgentCall(fixture, role)})
	}
	for _, call := range calls {
		if err := call.run(); !errors.Is(err, context.Canceled) && !errors.Is(err, ErrRunnersStopped) {
			t.Fatalf("%s was not blocked by stop flag: %v", call.name, err)
		}
		t.Logf("BLOCKED %s", call.name)
	}
}

func assertSchedulerDoesNotStartAgent(t *testing.T, e *Engine, ctx context.Context, marker string, p model.Provider) {
	t.Helper()
	// Exercise the actual scheduler across three 2-second dispatch ticks.
	runCtx, cancel := context.WithTimeout(ctx, 6500*time.Millisecond)
	defer cancel()
	e.Run(runCtx)
	markers, _ := filepath.Glob(filepath.Join(e.Root, "sandbox", "*", "home", "agent-starts.log"))
	if _, err := os.Stat(marker); !os.IsNotExist(err) || len(markers) > 0 {
		t.Fatalf("agent process launched while stopped: %v", err)
	}
	sessions, err := e.Store.List(ctx, "sessions")
	if err != nil || len(sessions) != 0 {
		t.Fatalf("stopped runner created sessions: %d %v", len(sessions), err)
	}
	t.Log("STOPPED PROOF: 9 entry paths + 3 scheduler ticks; agent starts=0; sessions=0")
	setRunnerStopped(t, e, false)
	controlCtx, controlCancel := context.WithTimeout(ctx, 5*time.Second)
	defer controlCancel()
	_, controlErr := e.TestProviderConnection(controlCtx, p)
	t.Logf("positive control provider outcome: %v", controlErr)
	markers, _ = filepath.Glob(filepath.Join(e.Root, "sandbox", "*", "home", "agent-starts.log"))
	if len(markers) != 1 {
		t.Fatalf("positive control runtime markers: %v", markers)
	}
	starts, err := os.ReadFile(markers[0])
	if err != nil || string(starts) != "started\n" {
		t.Fatalf("positive control did not launch exactly one real child: %q %v", starts, err)
	}
	t.Log("ENABLED CONTROL: identical real process adapter; agent starts=1")
	setRunnerStopped(t, e, true)
}

func stoppedProviderConnection(e *Engine, ctx context.Context, p model.Provider) error {
	_, err := e.TestProviderConnection(ctx, p)
	return err
}
func stoppedProviderModels(e *Engine, ctx context.Context, p model.Provider) error {
	_, err := e.ProviderModels(ctx, p)
	return err
}

func stoppedAgentCall(f stoppedRunnerFixture, role string) func() error {
	return func() error {
		_, err := f.engine.agent(f.ctx, f.project, f.task, provider.Input{Role: role, CWD: f.path, Prompt: "Probe"})
		return err
	}
}
