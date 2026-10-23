package verification

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

// Bypassing execution.Command would let a committed check damage the host outside its worktree.
func TestCheckRunnerUsesSandboxProfile(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escaped")
	p, err := execution.ForPolicy(root, false, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := RunObservedWithProfile(context.Background(), root, []model.Check{{Name: "escape", Command: []string{"/usr/bin/touch", outside}}}, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || len(report.Results) != 1 || report.Results[0].Passed {
		t.Fatalf("outside write accepted: %+v", report)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatal("outside write occurred")
	}
}

// Check output grants must permit build artifacts without making tracked source writable.
func TestCheckOutputGrantKeepsSourceReadOnly(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	p, _ := execution.ForPolicy(root, true, "fixture", nil)
	report, err := RunObservedWithProfile(context.Background(), root, []model.Check{{Name: "build", OutputPaths: []string{"dist"}, Command: []string{"/bin/sh", "-c", "mkdir -p dist; printf artifact > dist/output; printf damaged > source.txt"}}}, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("source mutation succeeded")
	}
	b, _ := os.ReadFile(source)
	if string(b) != "original" {
		t.Fatal("source changed")
	}
	b, err = os.ReadFile(filepath.Join(root, "dist", "output"))
	if err != nil || string(b) != "artifact" {
		t.Fatalf("declared output failed: %s %v", b, err)
	}
}
