package process

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"whirlwind/internal/redaction"
)

// TestRegistryReapsOrphanBeforeRestart verifies restart recovery kills registered orphan processes and clears their registry entries.
func TestRegistryReapsOrphanBeforeRestart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", root)
	cmd := Command(context.Background(), "sleep", "60")
	if e := Start(cmd); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
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

func TestCleanupCallbacksWaitForOwnedProcessDrain(t *testing.T) {
	cmd := Command(context.Background(), "sh", "-c", "exit 0")
	var called atomic.Bool
	if err := OnCleanup(cmd, func() { called.Store(true) }); err != nil {
		t.Fatal(err)
	}
	if err := Forget(cmd); err != nil {
		t.Fatal(err)
	}
	if !called.Load() {
		t.Fatal("cleanup callback did not run after the owned process drained")
	}
}

func TestWaitForDrainSeparatesOutputDrainFromCredentialRelease(t *testing.T) {
	cmd := Command(context.Background(), "sh", "-c", "exit 0")
	var released atomic.Bool
	if err := OnCleanup(cmd, func() { released.Store(true) }); err != nil {
		t.Fatal(err)
	}
	if err := Start(cmd); err != nil {
		t.Fatal(err)
	}
	release, err := WaitForDrain(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if released.Load() {
		t.Fatal("credential cleanup ran before caller confirmed its output drain")
	}
	if release == nil {
		t.Fatal("verified process drain did not return a cleanup release")
	}
	release()
	if !released.Load() {
		t.Fatal("credential cleanup did not run after caller confirmed its output drain")
	}
}

func TestManagedStartPromotesCredentialRedactionUntilCleanup(t *testing.T) {
	const secret = "managed-process-secret-0123456789"
	releaseSecret, err := redaction.RegisterSecret(secret, 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command(context.Background(), "sh", "-c", "sleep 0.05")
	if err := OnCleanup(cmd, releaseSecret); err != nil {
		releaseSecret()
		t.Fatal(err)
	}
	if err := OnStart(cmd, func() error { return redaction.KeepSecretUntilReleased(secret) }); err != nil {
		_ = Forget(cmd)
		t.Fatal(err)
	}
	if err := Start(cmd); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := redaction.RedactText("event=" + secret); strings.Contains(got, secret) {
		t.Fatalf("started child lost credential redaction before cleanup: %q", got)
	}
	if err := Wait(cmd); err != nil {
		t.Fatal(err)
	}
	if got := redaction.RedactText(secret); got != secret {
		t.Fatalf("credential redaction remained after process cleanup: %q", got)
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
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	file := filepath.Join(root, token(cmd)+".json")
	b, _ := json.Marshal(registration{Token: token(cmd)})
	if err := os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Reap(root); err != nil {
		t.Fatalf("pre-registration recovery: %v", err)
	}
}
