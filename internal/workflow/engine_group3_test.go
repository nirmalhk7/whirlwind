package workflow

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// TestCriticalChangeWaitsForHumanBeforePR verifies critical-risk acceptance pauses for human approval before publication.
func TestCriticalChangeWaitsForHumanBeforePR(t *testing.T) {
	ctx := context.Background()
	e, task, _ := testEngine(t)
	p, _ := e.Project(ctx, task.ProjectID)
	p.Policy.RiskRules = []model.RiskRule{{Level: "critical", Paths: []string{resultFile}}}
	r, _ := e.Store.Get(ctx, "projects", p.ID)
	if err := e.Store.Put(ctx, "projects", p.ID, p, r.Version); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 6; n++ {
		if err := e.Step(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	r, _ = e.Store.Get(ctx, "tasks", task.ID)
	done, _ := store.Decode[model.Task](r)
	if done.Stage != "approval" || done.PR != "" || done.Approved {
		t.Fatalf("approval bypass: %+v", done)
	}
}

// TestProductiveReviewHasNoPassCap verifies a task can be accepted after more than one hundred review passes.
func TestProductiveReviewHasNoPassCap(t *testing.T) {
	ctx := context.Background()
	e, task, _ := testEngine(t)
	for n := 0; n < 2; n++ {
		if err := e.Step(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	done, _ := store.Decode[model.Task](r)
	done.ReviewPasses = 100
	if err := e.Store.Put(ctx, "tasks", task.ID, done, r.Version); err != nil {
		t.Fatal(err)
	}
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Store.Get(ctx, "tasks", task.ID)
	done, _ = store.Decode[model.Task](r)
	if done.Stage != "submitting" || done.ReviewPasses != 101 {
		t.Fatalf("review cap remains: %+v", done)
	}
}

// TestPRDescriptionListsEachUnitOnce verifies the published PR description lists each implementation unit once.
func TestPRDescriptionListsEachUnitOnce(t *testing.T) {
	ctx := context.Background()
	e, task, _ := testEngine(t)
	recorded := &recordingGitHub{}
	e.GitHub = recorded
	for n := 0; n < 7; n++ {
		if err := e.Step(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(recorded.prBody, "- add result.txt"); got != 1 {
		t.Fatalf("PR description lists implementation unit %d times; want once:\n%s", got, recorded.prBody)
	}
}

// TestRepeatedUnchangedFailuresEscalateAfterThreeRecoveries verifies repeated unchanged diffs exhaust three recoveries and pause the task.
func TestRepeatedUnchangedFailuresEscalateAfterThreeRecoveries(t *testing.T) {
	ctx := context.Background()
	e, task, repo := testEngine(t)
	task.Worktree = repo
	task.Base = "HEAD"
	task.Stage = "repair"
	for n := 0; n < 15; n++ {
		if err := e.progress(ctx, &task); err != nil {
			t.Fatal(err)
		}
	}
	if task.Stage != "paused" || task.Recoveries != 3 {
		t.Fatalf("stall not escalated: %+v", task)
	}
}

type permissionAgent struct{}

// Run requests write permission using the canonical worktree path to exercise alias handling.
func (permissionAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	canonical, err := filepath.EvalSymlinks(in.CWD)
	if err != nil {
		return provider.Output{}, err
	}
	allowed, err := h.Permission(ctx, provider.Permission{Tool: "fs/write_text_file", Paths: []string{filepath.Join(canonical, resultFile)}})
	if err != nil {
		return provider.Output{}, err
	}
	if !allowed {
		return provider.Output{}, fmt.Errorf("configured file permission denied")
	}
	return provider.Output{Text: "allowed"}, nil
}

// TestPermissionNormalizesWorktreeAliases verifies workspace aliases resolve to the canonical path before permission checks.
func TestPermissionNormalizesWorktreeAliases(t *testing.T) {
	e, task, repo := testEngine(t)
	p, _ := e.Project(context.Background(), task.ProjectID)
	e.Adapter = func(model.Provider) provider.Adapter { return permissionAgent{} }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: repo, Prompt: "test", ReadOnly: false, SessionID: ""})
	if err != nil || out.Text != "allowed" {
		t.Fatalf("alias permission: %+v %v", out, err)
	}
}
