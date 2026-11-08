package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

func TestExactCandidateDiffUsesReviewedCommitAndRejectsChangedCheckout(t *testing.T) {
	worktree := t.TempDir()
	mustCommand(t, worktree, "git", "init", "--quiet")
	mustCommand(t, worktree, "git", "config", "user.email", "test@example.invalid")
	mustCommand(t, worktree, "git", "config", "user.name", "Test")
	source := filepath.Join(worktree, "source.txt")
	if err := os.WriteFile(source, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mustCommand(t, worktree, "git", "add", "source.txt")
	mustCommand(t, worktree, "git", "commit", "--quiet", "-m", "base")
	base := strings.TrimSpace(mustCommand(t, worktree, "git", "rev-parse", "HEAD"))
	if err := os.WriteFile(source, []byte("after exact review\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mustCommand(t, worktree, "git", "commit", "--quiet", "-am", "candidate")
	reviewed := strings.TrimSpace(mustCommand(t, worktree, "git", "rev-parse", "HEAD"))
	task := model.Task{Worktree: worktree, Base: base, ReviewedCommit: reviewed}
	diff, err := exactCandidateDiffPage(context.Background(), task, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Content, "+after exact review") || strings.Contains(diff.Content, "+unstaged") {
		t.Fatalf("diff did not describe the pinned candidate: %s", diff.Content)
	}
	if err := os.WriteFile(source, []byte("unstaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := exactCandidateDiffPage(context.Background(), task, 0); err == nil || !strings.Contains(err.Error(), "worktree changed after review") {
		t.Fatalf("changed checkout was accepted for local approval: %v", err)
	}
}

func TestBoundedOutputKeepsFixedCapacity(t *testing.T) {
	output := &boundedOutput{limit: 4}
	if _, err := output.Write([]byte("123456")); err != nil {
		t.Fatal(err)
	}
	if len(output.data) != 4 || !output.overflow {
		t.Fatalf("bounded output kept %d bytes and overflow=%t", len(output.data), output.overflow)
	}
}

func TestExactCandidateDiffRejectsMovedHeadAndOversizedOutput(t *testing.T) {
	worktree := t.TempDir()
	mustCommand(t, worktree, "git", "init", "--quiet")
	mustCommand(t, worktree, "git", "config", "user.email", "test@example.invalid")
	mustCommand(t, worktree, "git", "config", "user.name", "Test")
	source := filepath.Join(worktree, "source.txt")
	if err := os.WriteFile(source, []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mustCommand(t, worktree, "git", "add", "source.txt")
	mustCommand(t, worktree, "git", "commit", "--quiet", "-m", "base")
	base := strings.TrimSpace(mustCommand(t, worktree, "git", "rev-parse", "HEAD"))
	largeLine := strings.Repeat("large diff line ", 256) + "\n"
	largeContent := strings.Repeat(largeLine, (finalizationDiffPageBytes*18)/len(largeLine))
	if err := os.WriteFile(source, []byte(largeContent), 0600); err != nil {
		t.Fatal(err)
	}
	mustCommand(t, worktree, "git", "commit", "--quiet", "-am", "large candidate")
	reviewed := strings.TrimSpace(mustCommand(t, worktree, "git", "rev-parse", "HEAD"))
	task := model.Task{Worktree: worktree, Base: base, ReviewedCommit: reviewed}
	var diff strings.Builder
	pages := 0
	offset := int64(0)
	for {
		page, err := exactCandidateDiffPage(context.Background(), task, offset)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page.Content) > finalizationDiffLineLimit || page.Offset != offset || page.NextOffset < offset {
			t.Fatalf("diff page exceeded its bounded cursor contract: %+v", page)
		}
		diff.WriteString(page.Content)
		if !page.HasMore {
			break
		}
		if page.NextOffset <= offset {
			t.Fatalf("diff continuation made no progress: %+v", page)
		}
		offset = page.NextOffset
	}
	if pages < 2 || diff.Len() <= finalizationDiffPageBytes*16 {
		t.Fatalf("large exact candidate diff did not paginate fully: pages=%d bytes=%d", pages, diff.Len())
	}
	mustCommand(t, worktree, "git", "commit", "--allow-empty", "--quiet", "-m", "moved head")
	if _, err := exactCandidateDiffPage(context.Background(), task, 0); err == nil || !strings.Contains(err.Error(), "candidate changed after review") {
		t.Fatalf("moved HEAD was accepted for local approval: %v", err)
	}
}
