package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// Run simulates either quota exhaustion or a successful continuation by a backup provider.
func (a quotaAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	if a.fail {
		return provider.Output{}, fmt.Errorf("quota exhausted")
	}
	return provider.Output{Text: "continued"}, nil
}

// TestQuotaFailureHandsOffToAnotherProvider verifies quota exhaustion switches providers while continuing the same workspace.
func TestQuotaFailureHandsOffToAnotherProvider(t *testing.T) {
	ctx := context.Background()
	e, task, repo := testEngine(t)
	p, _ := e.Project(ctx, task.ProjectID)
	p.Policy.Providers = []string{"fake", "backup"}
	if err := e.Store.Put(ctx, "providers", "backup", model.Provider{ID: "backup", Available: true, Quota: "unknown", Capabilities: []string{"code"}}, 0); err != nil {
		t.Fatal(err)
	}
	e.Adapter = func(p model.Provider) provider.Adapter { return quotaAgent{fail: p.ID == "fake"} }
	out, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: repo, Prompt: "continue", ReadOnly: false, SessionID: ""})
	if err != nil || out.Text != "continued" {
		t.Fatalf("handoff: %+v %v", out, err)
	}
}

type unitAgent struct{}

// Run writes and commits multiple fixture files to exercise integration of every unit commit.
func (unitAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	unit := filepath.Base(in.CWD)
	if in.Role == "code" {
		for _, suffix := range []string{"one", "two"} {
			file := unit + "-" + suffix + ".txt"
			if err := os.WriteFile(filepath.Join(in.CWD, file), []byte(file), 0600); err != nil {
				return provider.Output{}, err
			}
			for _, args := range [][]string{{"add", file}, {"commit", "-m", file}} {
				if b, err := exec.Command("git", append([]string{"-C", in.CWD}, args...)...).CombinedOutput(); err != nil {
					return provider.Output{}, fmt.Errorf("%s: %w", b, err)
				}
			}
		}
		if err := os.WriteFile(filepath.Join(in.CWD, resultFile), []byte("ok"), 0600); err != nil {
			return provider.Output{}, err
		}
	}
	return provider.Output{Text: "done"}, nil
}

// TestParallelUnitsIntegrateAllCommits verifies serial integration retains every commit produced by independent parallel units.
func TestParallelUnitsIntegrateAllCommits(t *testing.T) {
	ctx := context.Background()
	e, task, _ := testEngine(t)
	task.Stage = "coding"
	task.Plan = &model.Plan{Objective: "two units", AcceptanceCriteria: []string{"both complete"}, Units: []model.Unit{{ID: "a", Objective: "first"}, {ID: "b", Objective: "second"}}}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	if err := e.Store.Put(ctx, "tasks", task.ID, task, r.Version); err != nil {
		t.Fatal(err)
	}
	e.Adapter = func(model.Provider) provider.Adapter { return unitAgent{} }
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Store.Get(ctx, "tasks", task.ID)
	done, _ := store.Decode[model.Task](r)
	for _, name := range []string{"a-one.txt", "a-two.txt", "b-one.txt", "b-two.txt"} {
		if _, err := os.Stat(filepath.Join(done.Worktree, name)); err != nil {
			t.Fatalf("lost unit commit %s: %v", name, err)
		}
	}
	if done.Stage != "review" || !done.ChecksPassed {
		t.Fatalf("%+v", done)
	}
}

type partialFailureAgent struct {
	started chan struct{}
	err     error
}

// Run fails one unit while another completes, checking that errors do not cancel sibling work.
func (a partialFailureAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	if filepath.Base(in.CWD) == "fail" {
		select {
		case <-a.started:
			return provider.Output{}, a.err
		case <-ctx.Done():
			return provider.Output{}, ctx.Err()
		}
	}
	close(a.started)
	select {
	case <-ctx.Done():
		return provider.Output{}, ctx.Err()
	case <-time.After(250 * time.Millisecond):
		return (fakeAgent{9}).Run(ctx, in, h)
	}
}

// TestUnitFailureWaitsForSiblingCheckpoint preserves successful work for recovery even when another unit fails.
func TestUnitFailureWaitsForSiblingCheckpoint(t *testing.T) {
	e, task, _ := testEngine(t)
	task.Stage = "coding"
	task.Plan = &model.Plan{Objective: "two units", Units: []model.Unit{{ID: "fail", Objective: "fail deliberately"}, {ID: "keep", Objective: "complete sibling"}}}
	failure := errors.New("unit failed")
	agent := partialFailureAgent{started: make(chan struct{}), err: failure}
	e.Adapter = func(model.Provider) provider.Adapter { return agent }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.code(ctx, project, &task); !errors.Is(err, failure) {
		t.Fatalf("expected unit failure, got %v", err)
	}
	record, err := e.Store.Get(ctx, "units", fmt.Sprintf("%s:%d:keep", task.ID, task.PlanVersion))
	if err != nil {
		t.Fatal(err)
	}
	unit, err := store.Decode[model.Unit](record)
	if err != nil || unit.Stage != "complete" || unit.Commit == "" {
		t.Fatalf("successful sibling checkpoint lost: %+v, %v", unit, err)
	}
}
