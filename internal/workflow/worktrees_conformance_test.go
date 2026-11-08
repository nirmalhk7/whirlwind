package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"whirlwind/internal/github"
)

func TestWorktreeRejectsExistingForeignRepositoryWithoutChangingIt(t *testing.T) {
	repo := newSubmissionRepository(t)
	foreign := repo.target
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, foreign, "init")
	submissionGit(t, foreign, "config", "user.name", "Foreign")
	submissionGit(t, foreign, "config", "user.email", "foreign@localhost")
	marker := filepath.Join(foreign, "user-data.txt")
	if err := os.WriteFile(marker, []byte("preserve this work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, foreign, "add", "user-data.txt")
	submissionGit(t, foreign, "commit", "-m", "foreign user work")
	before := submissionGit(t, foreign, "rev-parse", "HEAD")
	err := (&Engine{}).worktree(context.Background(), repo.source, foreign, "codex/reuse", repo.base)
	if err == nil {
		t.Fatal("foreign repository was accepted as the planned worktree")
	}
	if after := submissionGit(t, foreign, "rev-parse", "HEAD"); after != before {
		t.Fatalf("foreign worktree history changed from %s to %s", before, after)
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "preserve this work\n" {
		t.Fatalf("foreign user data was changed: %q err=%v", contents, err)
	}
	if _, err := github.Run(context.Background(), foreign, "git", "status", "--porcelain"); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeReusesOnlyMatchingOwnedBranchAndPreservesDirtyFiles(t *testing.T) {
	repo := newSubmissionRepository(t)
	branch := "codex/reuse"
	if _, err := github.Run(context.Background(), repo.source, "git", "worktree", "add", "-b", branch, repo.target, repo.base); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(repo.target, "user-draft.txt")
	if err := os.WriteFile(marker, []byte("keep this draft\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (&Engine{}).worktree(context.Background(), repo.source, repo.target, branch, repo.base); err != nil {
		t.Fatalf("matching owned worktree was not reused: %v", err)
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "keep this draft\n" {
		t.Fatalf("dirty user work was changed during reuse: %q err=%v", contents, err)
	}
}
