package execution

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOfflineACPStagedWriteRequiresRoleAndUnchangedTarget(t *testing.T) {
	data := t.TempDir()
	cwd := filepath.Join(data, "worktrees", "plan", "unit")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cwd, "README.md")
	if err := os.WriteFile(target, []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	profile := Profile{CWD: cwd, AllowedPaths: []string{"README.md"}, offlineACP: &offlineACPProfile{}}
	if _, err := profile.offlineACPWriteStage(OfflineACPRegistry(data)); err == nil {
		t.Fatal("writable fixture without a Coding role")
	}
	profile.BindOfflineACPRole("code", "")
	for _, path := range []string{"package.json", "../README.md", "*.md", ".git"} {
		profile.AllowedPaths = []string{path}
		if _, err := profile.offlineACPWriteStage(OfflineACPRegistry(data)); err == nil {
			t.Fatalf("accepted forbidden Coder path %q", path)
		}
	}
	profile.AllowedPaths = []string{"README.md"}
	stage, err := profile.offlineACPWriteStage(OfflineACPRegistry(data))
	if err != nil {
		t.Fatal(err)
	}
	conn := OfflineACPConnection{stage: stage}
	if err := os.WriteFile(stage.source, []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := conn.FinishWrites(true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "base\n" {
		t.Fatal("copied files before verified cleanup", err)
	}
	if err := os.WriteFile(target, []byte("changed concurrently\n"), 0600); err != nil {
		t.Fatal(err)
	}
	conn.cleanupVerified = true
	if err := conn.FinishWrites(true); err == nil {
		t.Fatal("imported stale staged file")
	}
	raw, err = os.ReadFile(target)
	if err != nil || string(raw) != "changed concurrently\n" {
		t.Fatal("overwrote changed candidate", err)
	}
}

func TestOfflineACPPlannerOutputRejectsSymlinkAndScopeEscape(t *testing.T) {
	data := t.TempDir()
	cwd := filepath.Join(data, "planning", "plan", "v1")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(cwd, ".whirlwind-plan-output-fixture")
	if err := os.Symlink(data, out); err != nil {
		t.Fatal(err)
	}
	p := Profile{ReadOnly: true, CWD: cwd, WriteRoots: []string{out}, offlineACP: &offlineACPProfile{}}
	p.BindOfflineACPRole("plan", "idea_planning")
	if _, err := p.offlineACPWriteStage(OfflineACPRegistry(data)); err == nil {
		t.Fatal("mounted symbolic Planner output")
	}
	p.WriteRoots = []string{data}
	if _, err := p.offlineACPWriteStage(OfflineACPRegistry(data)); err == nil {
		t.Fatal("mounted supervisor state as Planner output")
	}
}
