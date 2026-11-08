package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/github"
)

// A transient fetch must not prevent discovery from resolving the remote base.
func TestDiscoveryBaseRecoversInterruptedFetch(t *testing.T) {
	e, task, repo := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command(realGit, "-C", repo, "remote", "add", "origin", repo).CombinedOutput(); err != nil {
		t.Fatalf("remote: %s %v", b, err)
	}
	dir := t.TempDir()
	state := filepath.Join(dir, "attempt")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = fetch ] && [ ! -f \"$FETCH_STATE\" ]; then touch \"$FETCH_STATE\"; echo 'Connection reset by 172.182.252.133 port 22' >&2; exit 128; fi\nexec %q \"$@\"\n", realGit)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("FETCH_STATE", state)
	p.DefaultBranch = "main"
	commit, err := e.base(ctx, p)
	if err != nil || len(strings.TrimSpace(commit)) != 40 {
		t.Fatalf("fetch failed to recover: %q %v", commit, err)
	}
}

func TestBaseUsesLocalDefaultBranchWhenOriginIsAbsent(t *testing.T) {
	e, task, repo := testEngine(t)
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	branch := submissionGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	commit := submissionGit(t, repo, "rev-parse", "HEAD")
	if _, err := github.Run(ctx, repo, "git", "remote", "add", "upstream", filepath.Join(t.TempDir(), "missing-remote")); err != nil {
		t.Fatal(err)
	}
	project.DefaultBranch = branch
	base, err := e.base(ctx, project)
	if err != nil {
		t.Fatalf("local repository base required nonexistent origin fetch: %v", err)
	}
	if base != commit {
		t.Fatalf("resolved base %s, want local branch commit %s", base, commit)
	}
}

func TestPlanningWorktreeKeepsOriginalBaseAfterDefaultBranchAdvances(t *testing.T) {
	e, task, repo := testEngine(t)
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	branch := submissionGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	project.DefaultBranch = branch
	step := &taskStep{Engine: e, ctx: ctx, project: project, task: task}
	path, err := step.planningWorktree()
	if err != nil {
		t.Fatal(err)
	}
	originalBase := submissionGit(t, path, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "advanced-default.txt"), []byte("new default branch state\n"), 0600); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, repo, "add", "advanced-default.txt")
	submissionGit(t, repo, "commit", "-m", "advance local default branch")
	pathAfterRestart, err := step.planningWorktree()
	if err != nil {
		t.Fatalf("retry followed the advanced default branch instead of the original planning snapshot: %v", err)
	}
	if pathAfterRestart != path || submissionGit(t, pathAfterRestart, "rev-parse", "HEAD") != originalBase {
		t.Fatalf("planning snapshot changed after default branch advanced: path=%s original=%s current=%s", pathAfterRestart, originalBase, submissionGit(t, pathAfterRestart, "rev-parse", "HEAD"))
	}
}
