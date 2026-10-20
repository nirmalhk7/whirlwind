package context

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
