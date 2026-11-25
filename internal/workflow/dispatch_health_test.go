package workflow

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	dispatchFixtureQueuedOperation = "queued-ideation"
)

func TestRunCycleReportsRecoveryBlockWithoutDispatchAndOnlyClearsAfterCheck(t *testing.T) {
	f := prepareNoveltyTest(t, nil)
	e, ctx := f.engine, t.Context()
	if e.DispatchHealth().Checked {
		t.Fatal("new supervisor claimed that recovery was already checked")
	}
	tasks, err := e.Store.List(ctx, "execution_plans")
	requireWorkflowSuccess(t, err)
	for _, record := range tasks {
		task, err := store.Decode[model.ExecutionPlan](record)
		requireWorkflowSuccess(t, err)
		task.Stage = "complete"
		requireWorkflowSuccess(t, e.Store.Put(ctx, "execution_plans", record.ID, task, record.Version))
	}
	settings := e.settings(ctx)
	settings.Ideation.Cron = ""
	setStopped := func(stopped bool) {
		t.Helper()
		settings.Stopped = stopped
		record, err := e.Store.Get(ctx, "settings", "global")
		requireWorkflowSuccess(t, err)
		requireWorkflowSuccess(t, e.Store.Put(ctx, "settings", "global", settings, record.Version))
	}
	setStopped(false)
	requireWorkflowSuccess(t, e.Store.Put(ctx, "operations", dispatchFixtureQueuedOperation, model.Operation{
		Kind: "ideation", Subject: f.project.ID, Stage: "queued", Input: map[string]any{"day": "2026-11-24", "fresh": true},
	}, 0))
	registry := filepath.Join(e.Root, "supervisor", "standalone")
	requireWorkflowSuccess(t, os.MkdirAll(registry, 0700))
	path := execution.StandaloneRegistryPath(registry)
	requireWorkflowSuccess(t, os.WriteFile(path, []byte(`{"version":1,"executions":[{"execution_id":"fixture","state":"active"}]}`), 0600))
	cycle := func() {
		e.runCycle(ctx, "", time.Now().Add(time.Hour))
		e.background.Wait()
	}
	cycle()
	blocked := e.DispatchHealth()
	if !blocked.Checked || !strings.Contains(blocked.BlockReason, "registered standalone runtime identity is incomplete or invalid") {
		t.Fatalf("real recovery failure not exposed: %+v", blocked)
	}
	queued, err := e.Store.Get(ctx, "operations", dispatchFixtureQueuedOperation)
	requireWorkflowSuccess(t, err)
	op, err := store.Decode[model.Operation](queued)
	requireWorkflowSuccess(t, err)
	if op.Stage != "queued" || f.fixture.generations != 0 {
		t.Fatalf("blocked recovery no longer fences dispatch: %+v", op)
	}
	requireWorkflowSuccess(t, os.WriteFile(path, []byte(`{"version":1,"executions":[]}`), 0600))
	setStopped(true)
	cycle()
	if e.DispatchHealth() != blocked {
		t.Fatal("stopping runners cleared a block without a verified recovery check")
	}
	setStopped(false)
	cycle()
	if state := e.DispatchHealth(); !state.Checked || state.BlockReason != "" {
		t.Fatalf("verified recovery did not clear the block: %+v", state)
	}
	finished, err := e.Store.Get(ctx, "operations", dispatchFixtureQueuedOperation)
	requireWorkflowSuccess(t, err)
	op, err = store.Decode[model.Operation](finished)
	requireWorkflowSuccess(t, err)
	if op.Stage != "complete" || f.fixture.generations != 1 {
		t.Fatalf("verified recovery did not resume normal dispatch: op=%+v generations=%d", op, f.fixture.generations)
	}
}

func TestDispatchBlockReasonIsTerminalSafeAndBounded(t *testing.T) {
	reason := dispatchRecoveryReason(errors.New("observed\x1b[0m\n" + strings.Repeat("é", 1000)))
	if strings.ContainsAny(reason, "\x1b\n") || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > 801 {
		t.Fatalf("unsafe recovery diagnostic: %q", reason)
	}
}
