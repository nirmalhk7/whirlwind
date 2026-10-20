package workflow

import (
	"context"
	"errors"
	"testing"
	"time"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// TestInterruptedIntegrationUsesDurableUnitCheckpoints verifies restarting coding restores unit checkpoints without duplicating integrated commits.
func TestInterruptedIntegrationUsesDurableUnitCheckpoints(t *testing.T) {
	ctx := context.Background()
	e, task, _ := testEngine(t)
	for n := 0; n < 2; n++ {
		if err := e.Step(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	done, _ := store.Decode[model.Task](r)
	before, err := github.Run(ctx, done.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	task.Stage = "coding"
	task.Plan = done.Plan
	for n := range task.Plan.Units {
		task.Plan.Units[n].Stage = ""
	}
	if err = e.Store.Put(ctx, "tasks", task.ID, task, r.Version); err != nil {
		t.Fatal(err)
	}
	if err = e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Store.Get(ctx, "tasks", task.ID)
	restored, _ := store.Decode[model.Task](r)
	after, err := github.Run(ctx, restored.Worktree, "git", "rev-parse", "HEAD")
	if err != nil || before != after || restored.Stage != "review" {
		t.Fatalf("checkpoint replay changed commits: %s %s %+v %v", before, after, restored, err)
	}
}

type waitingAgent struct{}

// Run blocks until context cancellation and returns its error to exercise run timeouts.
func (waitingAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	<-ctx.Done()
	return provider.Output{}, ctx.Err()
}

// TestConfiguredRunBudgetStopsAttempt verifies an explicit project run timeout cancels an agent attempt.
func TestConfiguredRunBudgetStopsAttempt(t *testing.T) {
	e, task, repo := testEngine(t)
	p, _ := e.Project(context.Background(), task.ProjectID)
	p.Policy.RunTimeoutSeconds = 1
	e.Adapter = func(model.Provider) provider.Adapter { return waitingAgent{} }
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	_, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: repo, Prompt: "test", ReadOnly: false, SessionID: ""})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("run budget not enforced: %v %s", err, time.Since(start))
	}
}
