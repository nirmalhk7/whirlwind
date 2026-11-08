package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	if err := e.Store.Put(ctx, "providers", "backup", model.Provider{ID: "backup", Kind: "acp", Available: true, Quota: "unknown", Capabilities: []string{"code"}}, 0); err != nil {
		t.Fatal(err)
	}
	putFixtureAgent(t, e.Store, "code", model.AgentConfig{Role: "code", Chain: []model.AgentChoice{{Provider: "fake", Model: "fixture-model"}, {Provider: "backup", Model: "backup-model"}}})
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

// TestParallelUnitsIntegrateAllCommits verifies every approved unit checkpoint reaches the isolated candidate before mandatory verification.
func TestParallelUnitsIntegrateAllCommits(t *testing.T) {
	ctx := context.Background()
	e, task, project, ready := approvedUnitCheckpointFixture(t)
	project.Policy.RunTimeoutSeconds = 60
	e.Adapter = func(model.Provider) provider.Adapter { return unitAgent{} }
	if err := e.prepareWorkspace(ctx, project, &task); err != nil {
		t.Fatal(err)
	}
	if err := e.runUnits(ctx, project, &task, ready); err != nil {
		t.Fatalf("run approved implementation units: %v", err)
	}
	if err := e.integrateUnits(ctx, project, &task, ready, map[string]bool{}); err != nil {
		t.Fatalf("integrate all unit commits: %v", err)
	}
	validateIntegratedUnitCommits(t, task)
	for _, name := range []string{"a-one.txt", "a-two.txt", "b-one.txt", "b-two.txt", resultFile} {
		if _, err := os.Stat(filepath.Join(task.Worktree, name)); err != nil {
			t.Fatalf("lost integrated unit change %s: %v", name, err)
		}
	}
	if len(task.Handoff.Commits) != len(task.Plan.Units) || len(slices.Compact(slices.Clone(task.Handoff.Commits))) != len(task.Handoff.Commits) {
		t.Fatalf("unit commit handoff omitted or duplicated a checkpoint: %+v", task.Handoff.Commits)
	}
	if task.ChecksPassed || task.Stage != "coding" {
		t.Fatalf("unit integration bypassed mandatory candidate verification: %+v", task)
	}
	publications, err := e.Store.List(ctx, "publications")
	if err != nil || len(publications) != 0 || task.PR != "" {
		t.Fatalf("unit integration attempted remote publication: PR=%q records=%d err=%v", task.PR, len(publications), err)
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
	task.Plan = &model.Plan{Objective: "two units", Units: []model.Unit{{ID: "fail", Objective: "fail deliberately", AllowedPaths: []string{"failed.txt"}}, {ID: "keep", Objective: "complete sibling", AllowedPaths: []string{resultFile}}}}
	failure := errors.New("unit failed")
	agent := partialFailureAgent{started: make(chan struct{}), err: failure}
	e.Adapter = func(model.Provider) provider.Adapter { return agent }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Policy.ParallelUnits = true
	project.Policy.RunTimeoutSeconds = 60
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
