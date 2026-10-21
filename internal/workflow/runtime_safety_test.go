package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const runtimeToolEvent = "whirlwind/tool"

func TestUnsettledToolPausesInsteadOfReplaying(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, _ provider.Input, h provider.Hooks) (provider.Output, error) {
			calls++
			for _, id := range []string{"a", "b", "c"} {
				h.Event(runtimeToolEvent, model.ToolActivity{ID: id, Kind: "execute", Status: "failed", InputHash: "same", OutputHash: "same"})
			}
			h.Event(runtimeToolEvent, model.ToolActivity{ID: "external-write", Kind: "execute", Status: "in_progress"})
			return provider.Output{}, errors.New("interrupted")
		})
	}
	_, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "fix issue"})
	if err == nil || calls != 1 {
		t.Fatalf("uncertain action replayed: %v calls=%d", err, calls)
	}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	saved, _ := store.Decode[model.Task](r)
	if saved.Stage != "paused" {
		t.Fatal("uncertain action did not pause task")
	}
}

func TestZeroContextDoesNotRotateTinyWindow(t *testing.T) {
	if highContext(&model.ContextUsage{Used: 0, Size: 1}) {
		t.Fatal("zero usage marked full")
	}
}

func TestVerificationStallSharesRecoveryAllowance(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	task.Worktree = cwd
	task.Base = "HEAD"
	task.Stage = "repair"
	key := task.ID + ":" + fmt.Sprint(task.PlanVersion)
	if err := e.Store.Put(ctx, "runtime_recoveries", key, 2, 0); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if err := e.progress(ctx, &task); err != nil {
			t.Fatal(err)
		}
	}
	if task.Stage != "paused" {
		t.Fatal("verification granted extra recovery after tool recovery exhaustion")
	}
}

func TestVerificationStallPersistsFreshSessionCheckpoint(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	task.Worktree = cwd
	task.Base = "HEAD"
	task.Stage = "repair"
	for range 5 {
		if err := e.progress(ctx, &task); err != nil {
			t.Fatal(err)
		}
	}
	run := &agentRun{Engine: e, ctx: ctx, task: task, input: provider.Input{Role: "code", CWD: cwd}}
	state, _, err := run.loadRuntime()
	if err != nil {
		t.Fatal(err)
	}
	count, _, err := run.recoveryCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || state.Generation != 1 || state.CheckpointID == "" {
		t.Fatalf("missing durable recovery: %+v count=%d", state, count)
	}
}

func TestTransportFailureWithActiveToolDoesNotRetry(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, _ provider.Input, h provider.Hooks) (provider.Output, error) {
			calls++
			h.Event(runtimeToolEvent, model.ToolActivity{ID: "publish", Kind: "execute", Status: "in_progress"})
			return provider.Output{}, io.EOF
		})
	}
	_, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "fix issue"})
	if err == nil || calls != 1 {
		t.Fatalf("active external action retried: %v calls=%d", err, calls)
	}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	saved, _ := store.Decode[model.Task](r)
	if saved.Stage != "paused" {
		t.Fatal("uncertain transport failure did not pause")
	}
}
