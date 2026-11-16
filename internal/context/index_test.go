package context

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	workingGitignorePath = ".gitignore"
	workingTrackedPath   = "src/tracked.ts"
	workingDeletedPath   = "src/deleted.go"
	workingIgnoredPath   = "ignored/secret.ts"
	workingOutsidePath   = "src/outside.ts"
	workingOutsideName   = "outside.ts"
	workingLoosePath     = "tracked.ts"
	workingUntrackedPath = "src/new.tsx"
)

// testGit runs a Git fixture command and fails the test with its output on error.
func testGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%s %v", b, e)
	}
}

// TestIncrementalIndexUpdatesOnlyChangedEntries verifies new commits refresh changed index entries while preserving unchanged files.
func TestIncrementalIndexUpdatesOnlyChangedEntries(t *testing.T) {
	d := t.TempDir()
	testGit(t, d, "init")
	if err := os.WriteFile(filepath.Join(d, "a.go"), []byte("package a\nfunc Login() {}\n// todo improve auth\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testGit(t, d, "add", ".")
	testGit(t, d, "commit", "-m", "initial")
	idx, e := Build(context.Background(), d, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(idx.Files) != 1 || len(idx.Files["a.go"].Symbols) != 1 {
		t.Fatalf("%+v", idx)
	}
	if err := os.WriteFile(filepath.Join(d, "a.go"), []byte("package a\nfunc Logout() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testGit(t, d, "add", ".")
	testGit(t, d, "commit", "-m", "update")
	next, e := Build(context.Background(), d, &idx)
	if e != nil || next.Version != 2 || next.Files["a.go"].Symbols[0] != "Logout" {
		t.Fatalf("%+v %v", next, e)
	}
	results := next.Search("Logout", 3)
	if len(results) != 1 {
		t.Fatal(results)
	}
}

func TestCaptureWorkingTreeOverlaysEligibleSourcesWithoutChangingHeadIndex(t *testing.T) {
	dir, head := newWorkingTreeFixture(t)
	writeWorkingFile(t, dir, workingTrackedPath, "export const value = 'worktree';\n")
	writeWorkingFile(t, dir, workingUntrackedPath, "export function NewPanel() { return null; }\n")
	if err := os.Remove(filepath.Join(dir, workingDeletedPath)); err != nil {
		t.Fatal(err)
	}
	got, err := CaptureWorkingTree(context.Background(), dir, head)
	if err != nil {
		t.Fatal(err)
	}
	assertWorkingTreeCapture(t, got, head)
}

func assertWorkingTreeCapture(t *testing.T, got, head Index) {
	t.Helper()
	if got.Commit != head.Commit {
		t.Fatalf("working tree changed pinned HEAD revision: got %q want %q", got.Commit, head.Commit)
	}
	if got.Files[workingTrackedPath].Text != "export const value = 'worktree';\n" {
		t.Fatalf("tracked edit missing from working-tree index: %+v", got.Files[workingTrackedPath])
	}
	if got.Files[workingUntrackedPath].Text != "export function NewPanel() { return null; }\n" {
		t.Fatalf("untracked source missing from working-tree index: %+v", got.Files[workingUntrackedPath])
	}
	if got.WorkingTreeFiles[workingTrackedPath] == "" {
		t.Fatal("working-tree content hash missing for a tracked file hidden from Git diff")
	}
	assertIndexPathsAbsent(t, got, workingDeletedPath, ".env", workingIgnoredPath, workingOutsidePath)
	if head.Files[workingTrackedPath].Text != "export const value = 'head';\n" {
		t.Fatalf("HEAD index was mutated by working-tree capture: %+v", head.Files[workingTrackedPath])
	}
}

func newWorkingTreeFixture(t *testing.T) (string, Index) {
	t.Helper()
	dir := t.TempDir()
	testGit(t, dir, "init")
	for _, name := range []string{"src", "ignored"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeWorkingFile(t, dir, workingGitignorePath, ".env\nignored/\n")
	for path, content := range map[string]string{
		workingTrackedPath: "export const value = 'head';\n",
		workingDeletedPath: "package old\n",
		".env":             "API_TOKEN=never-index-this\n",
		workingIgnoredPath: "export const ignored = true;\n",
	} {
		writeWorkingFile(t, dir, path, content)
	}
	outside := filepath.Join(t.TempDir(), workingOutsideName)
	writeWorkingFile(t, filepath.Dir(outside), workingOutsideName, "export const privateOutside = true;\n")
	if err := os.Symlink(outside, filepath.Join(dir, workingOutsidePath)); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", workingGitignorePath, "src", workingDeletedPath)
	testGit(t, dir, "add", "-f", ".env", workingIgnoredPath)
	testGit(t, dir, "commit", "-m", "initial")
	head, err := Build(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return dir, head
}

func writeWorkingFile(t *testing.T, root, path, content string) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertIndexPathsAbsent(t *testing.T, index Index, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, exists := index.Files[path]; exists {
			t.Errorf("unsafe, ignored, or deleted path %q remained indexed", path)
		}
	}
}

func TestCaptureWorkingTreeExcludesSymlinkAliases(t *testing.T) {
	dir := t.TempDir()
	testGit(t, dir, "init")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	writeWorkingFile(t, dir, workingGitignorePath, ".env\n")
	writeWorkingFile(t, dir, ".env", "API_TOKEN=do-not-read\n")
	outside := filepath.Join(t.TempDir(), workingOutsideName)
	writeWorkingFile(t, filepath.Dir(outside), workingOutsideName, "export const outsideSecret = true;\n")
	for alias, target := range map[string]string{"src/alias.ts": filepath.Join(dir, ".env"), workingOutsidePath: outside} {
		if err := os.Symlink(target, filepath.Join(dir, filepath.FromSlash(alias))); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, dir, "add", workingGitignorePath, "src")
	testGit(t, dir, "add", "-f", ".env")
	testGit(t, dir, "commit", "-m", "symlink fixture")
	head, err := Build(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CaptureWorkingTree(context.Background(), dir, head)
	if err != nil {
		t.Fatal(err)
	}
	assertIndexPathsAbsent(t, got, "src/alias.ts", workingOutsidePath)
}

func TestCaptureWorkingTreeHashTracksAssumeUnchangedModification(t *testing.T) {
	dir := t.TempDir()
	testGit(t, dir, "init")
	writeWorkingFile(t, dir, workingLoosePath, "export const state = 'head';\n")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-m", "initial")
	head, err := Build(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "update-index", "--assume-unchanged", workingLoosePath)
	writeWorkingFile(t, dir, workingLoosePath, "export const state = 'worktree';\n")
	got, err := CaptureWorkingTree(context.Background(), dir, head)
	if err != nil {
		t.Fatal(err)
	}
	if got.Files[workingLoosePath].Text != "export const state = 'worktree';\n" || got.WorkingTreeFiles[workingLoosePath] == "" {
		t.Fatalf("captured source and worktree fingerprint disagree: entry=%+v worktree=%v", got.Files[workingLoosePath], got.WorkingTreeFiles)
	}
}

// TestEmbeddingEvaluationRequiresCoverage verifies improved recall alone cannot authorize embeddings when criterion coverage declines.
func TestEmbeddingEvaluationRequiresCoverage(t *testing.T) {
	if Benefit(0.5, 0.7, 1, 0.8) || !Benefit(0.5, 0.7, 1, 1) {
		t.Fatal("embedding activation gate")
	}
}

func TestActionMarkersIgnoreCase(t *testing.T) {
	for _, line := range []string{"// todo improve auth", "// fixme handle timeout"} {
		for _, variant := range []string{line, strings.ToUpper(line)} {
			entry := sourceEntry("service.txt", []byte(variant))
			if len(entry.TODOs) != 1 {
				t.Errorf("action marker missed: %q", variant)
			}
		}
	}
}
