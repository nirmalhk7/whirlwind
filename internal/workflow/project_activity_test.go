package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func setProjectPaused(t *testing.T, e *Engine, id string, paused bool) {
	t.Helper()
	r, err := e.Store.Get(context.Background(), "projects", id)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(r.Data, &p); err != nil {
		t.Fatal(err)
	}
	p["paused"] = paused
	if err := e.Store.Put(context.Background(), "projects", id, p, r.Version); err != nil {
		t.Fatal(err)
	}
}

func TestPausedProjectBlocksExecutionWithStaleProjectSnapshot(t *testing.T) {
	e, task, path := testEngine(t)
	ctx := context.Background()
	initial, _ := e.Store.Get(ctx, "tasks", task.ID)
	initialInbox, _ := e.Store.List(ctx, "inbox")
	p, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	setProjectPaused(t, e, p.ID, true)
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"task", func() error { return e.Step(ctx, task.ID) }},
		{"ideation", func() error { return e.Ideate(ctx, p, stoppedRunDay) }},
		{"queued ideation", func() error { return e.IdeateWhenAvailable(ctx, p, stoppedRunDay) }},
		{"agent", func() error { _, err := e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: path}); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil {
				t.Fatal("paused project allowed execution")
			}
		})
	}
	e.dispatch(ctx)
	e.background.Wait()
	e.Daily(ctx)
	for _, kind := range []string{"sessions", "source_cache", "schedules"} {
		r, err := e.Store.List(ctx, kind)
		if err != nil || len(r) != 0 {
			t.Fatalf("paused project produced %s: %d %v", kind, len(r), err)
		}
	}
	if notices, err := e.Store.List(ctx, "inbox"); err != nil || len(notices) != len(initialInbox) {
		t.Fatalf("pause added notifications: %d %v", len(notices), err)
	}
	current, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil || current.Version != initial.Version {
		t.Fatalf("task advanced while paused: %+v %v", current, err)
	}
	setProjectPaused(t, e, p.ID, false)
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatalf("resumed project blocked: %v", err)
	}
}

func TestProjectPauseDoesNotRecordScheduledFailure(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	initialInbox, _ := e.Store.List(ctx, "inbox")
	p, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	setProjectPaused(t, e, p.ID, true)
	// A scheduler may already have loaded the project when pause commits.
	if err := e.runDailyProject(ctx, p, stoppedRunDay); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"schedules"} {
		records, err := e.Store.List(ctx, kind)
		if err != nil || len(records) != 0 {
			t.Fatalf("pause recorded %s failure: %d %v", kind, len(records), err)
		}
	}
	if notices, err := e.Store.List(ctx, "inbox"); err != nil || len(notices) != len(initialInbox) {
		t.Fatalf("pause added failure notification: %d %v", len(notices), err)
	}
}

func TestStopProjectCancelsActiveAndQueuedWorkOnlyForThatProject(t *testing.T) {
	e, task, path := testEngine(t)
	initial, _ := e.Store.Get(context.Background(), "tasks", task.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	p, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	other := p
	other.ID = "other"
	if err := e.Store.Put(ctx, "projects", other.ID, other, 0); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 2)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			started <- in.Prompt
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	stepDone, otherDone, queuedDone := make(chan error, 1), make(chan error, 1), make(chan error, 1)
	go func() { stepDone <- e.Step(ctx, task.ID) }()
	go func() {
		_, err := e.agent(ctx, other, model.Task{ID: "other-task"}, provider.Input{Role: "plan", CWD: path, Prompt: "other"})
		otherDone <- err
	}()
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("agents did not start")
		}
	}
	lease, err := e.Store.Claim(ctx, "generation:"+p.ID, "another-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Store.Release(context.Background(), "generation:"+p.ID, "another-worker", lease.Fence) }()
	go func() { queuedDone <- e.IdeateWhenAvailable(ctx, p, stoppedRunDay) }()
	setProjectPaused(t, e, p.ID, true)
	e.StopProject(p.ID)
	for _, done := range []chan error{stepDone, queuedDone} {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrProjectPaused) {
				t.Fatalf("work not paused: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("project work did not stop")
		}
	}
	select {
	case err := <-otherDone:
		t.Fatalf("other project interrupted: %v", err)
	default:
	}
	current, err := e.Store.Get(context.Background(), "tasks", task.ID)
	if err != nil || current.Version != initial.Version {
		t.Fatalf("pause lost task checkpoint: %+v %v", current, err)
	}
	cancel()
	<-otherDone
}
