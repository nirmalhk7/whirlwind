package process

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestRegistryReapsOrphanBeforeRestart verifies restart recovery kills registered orphan processes and clears their registry entries.
func TestRegistryReapsOrphanBeforeRestart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", root)
	cmd := Command(context.Background(), "sleep", "60")
	if e := Start(cmd); e != nil {
		t.Fatal(e)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 1 {
		t.Fatalf("registry %v %v", entries, e)
	}
	if e = Reap(root); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 100; n++ {
		if syscall.Kill(cmd.Process.Pid, 0) != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	entries, _ = os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("orphan registration retained")
	}
	if _, e = os.Stat(filepath.Join(root, "invalid")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}

// TestReapHandlesCrashBeforePIDRegistration verifies token-based recovery finds an orphan launched before its PID was persisted.
func TestReapHandlesCrashBeforePIDRegistration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", root)
	cmd := Command(context.Background(), "sleep", "60")
	if err := Start(cmd); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	file := filepath.Join(root, token(cmd)+".json")
	b, _ := json.Marshal(registration{Token: token(cmd)})
	os.WriteFile(file, b, 0600)
	if err := Reap(root); err != nil {
		t.Fatalf("pre-registration recovery: %v", err)
	}
}
