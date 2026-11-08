package workflow

import (
	"context"
	"testing"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestReviewerWorktreesAreDistinctDetachedCopiesOfReviewedCommit(t *testing.T) {
	e, task, repo := testEngine(t)
	task.Worktree = repo
	step := &taskStep{Engine: e, task: task}
	commit, err := github.Run(context.Background(), task.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	status, err := github.Run(context.Background(), task.Worktree, "git", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil || status != "" {
		t.Fatalf("test source unexpectedly dirty: %q %v", status, err)
	}
	primary, err := step.reviewerWorktree(commit, "primary")
	if err != nil {
		t.Fatal(err)
	}
	security, err := step.reviewerWorktree(commit, "security")
	if err != nil {
		t.Fatal(err)
	}
	if primary == task.Worktree || security == task.Worktree || primary == security {
		t.Fatalf("reviewers shared a writer worktree: primary=%q security=%q source=%q", primary, security, task.Worktree)
	}
	for _, path := range []string{primary, security} {
		head, err := github.Run(context.Background(), path, "git", "rev-parse", "HEAD")
		if err != nil || head != commit {
			t.Fatalf("reviewer worktree %q is not pinned to %s: head=%q err=%v", path, commit, head, err)
		}
		if _, err := github.Run(context.Background(), path, "git", "symbolic-ref", "-q", "HEAD"); err == nil {
			t.Fatalf("reviewer worktree %q is not detached", path)
		}
	}
}

func TestPrimaryAndSpecialistReviewersGetReadOnlyIsolatedSnapshots(t *testing.T) {
	e, task, repo := testEngine(t)
	task.Worktree, task.PlanVersion, task.VerificationID, task.Risk = repo, 1, "verification", "high"
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	putFixtureAgent(t, e.Store, "review", model.AgentConfig{Role: "review", Chain: []model.AgentChoice{{Provider: "fake", Model: "fixture"}}})
	commit, err := github.Run(ctx, repo, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	provenance := model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Head: commit, VerificationID: task.VerificationID}
	if err := e.Store.Put(ctx, "verification", task.VerificationID, map[string]any{"provenance": provenance}, 0); err != nil {
		t.Fatal(err)
	}
	var calls []provider.Input
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
			calls = append(calls, in)
			output, err := (fakeAgent{9}).Run(ctx, in, hooks)
			output.AttemptID = in.Purpose
			return output, err
		})
	}
	step := &taskStep{Engine: e, ctx: ctx, project: project, task: task}
	primary, err := step.primaryReview("prompt", commit)
	if err != nil {
		t.Fatal(err)
	}
	if err := step.supplementReview("prompt", commit, &primary); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("expected primary and both specialist attempts, got %d", len(calls))
	}
	seen := map[string]bool{}
	for _, call := range calls {
		if !call.ReadOnly {
			t.Fatalf("review role was not read-only: %+v", call)
		}
		if call.CWD == repo || seen[call.CWD] {
			t.Fatalf("review roles shared a writer or reviewer worktree: %+v", calls)
		}
		seen[call.CWD] = true
		head, err := github.Run(ctx, call.CWD, "git", "rev-parse", "HEAD")
		if err != nil || head != commit {
			t.Fatalf("review role received the wrong snapshot: cwd=%s head=%s err=%v", call.CWD, head, err)
		}
	}
}
