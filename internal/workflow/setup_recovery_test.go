package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const setupRecoveryDay = "2026-11-04"

func TestScheduledSetupFailureBlocksAcrossDaysAndRestart(t *testing.T) {
	fixture := installSetupProbeOpenShell(t)
	e, task, _ := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	row, _ := e.Store.Get(ctx, "providers", "fake")
	source, _ := store.Decode[model.Provider](row)
	source.Command = []string{"/bin/sh", "-c", "exit 127"}
	source.Capabilities = append(source.Capabilities, "ideate")
	if err := e.Store.Put(ctx, "providers", source.ID, source, row.Version); err != nil {
		t.Fatal(err)
	}
	// Real adapter fails before initialize; no model response or network is required.
	calls := 0
	e.Adapter = func(p model.Provider) provider.Adapter { calls++; return provider.ProcessAdapter{Provider: p} }
	e.retryWait = func(context.Context, time.Duration) error { return nil }
	if err := e.runDailyProject(ctx, p, "2026-11-03"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("setup failure retried inference %d times", calls)
	}
	block, err := e.Store.Get(ctx, "setup_blocks", p.ID)
	if err != nil {
		t.Fatalf("no durable setup block: %v", err)
	}
	if err := e.runDailyProject(ctx, p, setupRecoveryDay); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(filepath.Join(e.Root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := New(reopened, e.GitHub, e.Root)
	restarted.Adapter = e.Adapter
	if err := restarted.runDailyProject(ctx, p, "2026-11-05"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("block lost across day/restart: %d", calls)
	}
	after, _ := reopened.Get(ctx, "setup_blocks", p.ID)
	if after.Version < block.Version {
		t.Fatal("block history lost")
	}
	assertSetupProbeRuntimeClean(t, restarted, p, fixture)
}

func TestLegacySetupFailureIsProbedBeforeScheduledInference(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	row, _ := e.Store.Get(ctx, "providers", "fake")
	source, _ := store.Decode[model.Provider](row)
	source.Command = []string{"/bin/sh", "-c", "exit 127"}
	source.Capabilities = append(source.Capabilities, "ideate")
	if err := e.Store.Put(ctx, "providers", source.ID, source, row.Version); err != nil {
		t.Fatal(err)
	}
	diagnostic := "provider transport: provider exited before response: exit status 127"
	if err := e.Store.Put(ctx, "schedules", p.ID+":2026-11-03", map[string]any{"stage": "failed", "project": p.ID, "error": diagnostic}, 0); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(e.Root, "ideation", p.ID)
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	attempt := model.Attempt{ProjectID: p.ID, Task: "ideation:" + p.ID + ":legacy", Provider: source.ID, Role: "ideate", Purpose: "generation", CWD: cwd, Finished: true, Status: "failed", Error: diagnostic}
	if err := e.Store.Put(ctx, "sessions", "legacy", attempt, 0); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e.Adapter = func(p model.Provider) provider.Adapter { calls++; return provider.ProcessAdapter{Provider: p} }
	if err := e.runDailyProject(ctx, p, setupRecoveryDay); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("legacy setup failure launched %d inference attempts", calls)
	}
	rows, _ := e.Store.List(ctx, "setup_probes")
	if len(rows) != 1 {
		t.Fatal("legacy setup was not probed")
	}
}

func TestPausedProjectDoesNotProbeLegacySetupBlock(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	r, _ := e.Store.Get(ctx, "projects", p.ID)
	p.Paused = true
	if err := e.Store.Put(ctx, "projects", p.ID, p, r.Version); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "schedules", p.ID+":2026-11-03", map[string]any{"stage": "failed", "error": "provider transport: provider exited before response: exit status 127"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.runDailyProject(ctx, p, setupRecoveryDay); err != nil {
		t.Fatalf("paused schedule should be skipped: %v", err)
	}
	rows, _ := e.Store.List(ctx, "setup_probes")
	if len(rows) != 0 {
		t.Fatal("paused project launched setup probes")
	}
}
