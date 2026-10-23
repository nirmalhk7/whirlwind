package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/github"
)

type submissionRepository struct {
	source, target, base, reviewed string
	groups                         []CommitGroup
}

func submissionGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := github.Run(context.Background(), dir, "git", args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func newSubmissionRepository(t *testing.T) submissionRepository {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "source")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, repo, "init")
	submissionGit(t, repo, "config", "user.name", "Test")
	submissionGit(t, repo, "config", "user.email", "test@localhost")
	submissionGit(t, repo, "config", "commit.gpgsign", "false")
	for name, content := range map[string]string{"feature.txt": "before\n", "delete.txt": "obsolete\n", "run.sh": "#!/bin/sh\n", "binary.dat": "\x00\x01\x02"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	submissionGit(t, repo, "add", "--all")
	submissionGit(t, repo, "commit", "-m", "base")
	base := submissionGit(t, repo, "rev-parse", "HEAD")
	for name, content := range map[string]string{"feature.txt": "after\n", "feature_test.txt": "test\n", "binary.dat": "\x00\xff\x03"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(repo, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, repo, "add", "--all")
	submissionGit(t, repo, "update-index", "--chmod=+x", "run.sh")
	if err := os.Chmod(filepath.Join(repo, "run.sh"), 0755); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, repo, "commit", "-m", "internal checkpoint")
	return submissionRepository{source: repo, target: filepath.Join(root, "submission"), base: base,
		reviewed: submissionGit(t, repo, "rev-parse", "HEAD"), groups: []CommitGroup{
			{Message: "Implement feature with regression coverage", Files: []string{"feature.txt", "feature_test.txt"}},
			{Message: "Update executable and binary; remove obsolete data", Files: []string{"delete.txt", "run.sh", "binary.dat"}},
		}}
}

func TestOrganizeSubmissionPreservesReviewedTreeAndSource(t *testing.T) {
	r := newSubmissionRepository(t)
	head, err := organizeSubmission(context.Background(), r.source, r.target, "codex/submission", r.base, r.reviewed, r.groups)
	if err != nil {
		t.Fatal(err)
	}
	if got := submissionGit(t, r.target, "rev-parse", "HEAD^{tree}"); got != submissionGit(t, r.source, "rev-parse", r.reviewed+"^{tree}") {
		t.Fatal("submission changed reviewed tree")
	}
	if got := submissionGit(t, r.source, "rev-parse", "HEAD"); got != r.reviewed {
		t.Fatal("source checkpoint history changed")
	}
	if got := submissionGit(t, r.target, "log", "--format=%s", "--reverse", r.base+"..HEAD"); got != r.groups[0].Message+"\n"+r.groups[1].Message {
		t.Fatalf("unexpected commit groups: %s", got)
	}
	if got := submissionGit(t, r.target, "log", "-1", "--format=%an <%ae>"); got != "Whirlwind <whirlwind@localhost>" {
		t.Fatalf("unexpected identity: %s", got)
	}
	if again, err := organizeSubmission(context.Background(), r.source, r.target, "codex/submission", r.base, r.reviewed, r.groups); err != nil || again != head {
		t.Fatalf("completed submission not idempotent: %s, %v", again, err)
	}
}

func TestOrganizeSubmissionRejectsInvalidGroupsBeforeCreatingTarget(t *testing.T) {
	r := newSubmissionRepository(t)
	tests := []struct {
		name   string
		groups []CommitGroup
	}{
		{"missing files", r.groups[:1]},
		{"duplicate files", append(append([]CommitGroup{}, r.groups...), r.groups[0])},
		{"unsafe path", []CommitGroup{{Message: "unsafe", Files: []string{"../feature.txt"}}}},
		{"empty message", []CommitGroup{{Files: []string{"feature.txt"}}}},
		{"no groups", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := organizeSubmission(context.Background(), r.source, r.target, "codex/submission", r.base, r.reviewed, tt.groups); err == nil {
				t.Fatal("invalid grouping accepted")
			}
			if _, err := os.Stat(r.target); !os.IsNotExist(err) {
				t.Fatal("invalid grouping created target")
			}
		})
	}
}

func TestOrganizeSubmissionRejectsStaleOrDirtySource(t *testing.T) {
	r := newSubmissionRepository(t)
	if _, err := organizeSubmission(context.Background(), r.source, r.target, "codex/submission", r.base, r.base, r.groups); err == nil {
		t.Fatal("stale reviewed commit accepted")
	}
	if err := os.WriteFile(filepath.Join(r.source, "untracked.txt"), []byte("unreviewed"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := organizeSubmission(context.Background(), r.source, r.target, "codex/submission", r.base, r.reviewed, r.groups); err == nil {
		t.Fatal("untracked source changes accepted")
	}
}

func TestOrganizeSubmissionResumesCompletedPrefix(t *testing.T) {
	r := newSubmissionRepository(t)
	submissionGit(t, r.source, "worktree", "add", "-b", "codex/submission", r.target, r.base)
	submissionGit(t, r.target, "restore", "--source", r.reviewed, "--staged", "--worktree", "--", "feature.txt", "feature_test.txt")
	submissionGit(t, r.target, "commit", "-m", r.groups[0].Message)
	first := submissionGit(t, r.target, "rev-parse", "HEAD")
	if _, err := organizeSubmission(context.Background(), r.source, r.target, "codex/submission", r.base, r.reviewed, r.groups); err != nil {
		t.Fatal(err)
	}
	if parent := submissionGit(t, r.target, "rev-parse", "HEAD^"); parent != first {
		t.Fatal("recovery replaced completed commit")
	}
}

func TestOrganizeSubmissionRejectsUnrelatedTargetHistory(t *testing.T) {
	r := newSubmissionRepository(t)
	submissionGit(t, r.source, "worktree", "add", "-b", "codex/submission", r.target, r.base)
	submissionGit(t, r.target, "commit", "--allow-empty", "-m", "unrelated")
	before := submissionGit(t, r.target, "rev-parse", "HEAD")
	if _, err := organizeSubmission(context.Background(), r.source, r.target, "codex/submission", r.base, r.reviewed, r.groups); err == nil {
		t.Fatal("unrelated target accepted")
	}
	if got := submissionGit(t, r.target, "rev-parse", "HEAD"); got != before {
		t.Fatal("unrelated target history changed")
	}
	if got := submissionGit(t, r.target, "status", "--porcelain"); strings.TrimSpace(got) != "" {
		t.Fatal("unrelated target files changed")
	}
}

func TestOrganizeSubmissionRejectsSplitFileDirectoryTransitionBeforeWriting(t *testing.T) {
	r := newSubmissionRepository(t)
	if err := os.Remove(filepath.Join(r.source, "feature.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(r.source, "feature.txt"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.source, "feature.txt", "nested.txt"), []byte("replacement\n"), 0644); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, r.source, "add", "--all")
	submissionGit(t, r.source, "commit", "-m", "directory transition")
	r.reviewed = submissionGit(t, r.source, "rev-parse", "HEAD")
	r.groups = append(r.groups, CommitGroup{Message: "Add nested file", Files: []string{"feature.txt/nested.txt"}})
	if _, err := organizeSubmission(context.Background(), r.source, r.target, "codex/submission", r.base, r.reviewed, r.groups); err == nil {
		t.Fatal("split directory transition accepted")
	}
	if _, err := os.Stat(r.target); !os.IsNotExist(err) {
		t.Fatal("unsafe grouping mutated target before validation")
	}
}
