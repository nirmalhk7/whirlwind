package provider

import (
	"context"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/process"
)

// A drained native process must not wait for unrelated provider probes to exit.
func TestNonoRPCDrainIgnoresUnrelatedActiveProcess(t *testing.T) {
	registry := t.TempDir()
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", registry)
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	unrelated := process.Command(ctx, "sleep", "30")
	if err := process.Start(unrelated); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = process.Wait(unrelated) }()
	profile, err := execution.ForPolicy(t.TempDir(), true, "policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = execution.PinRuntime(ctx, profile, "native-drain", 1, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), filepath.Join(t.TempDir(), "executions"))
	if err != nil {
		t.Fatal(err)
	}
	rpc, err := StartWithProfile(ctx, []string{"/bin/cat"}, profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rpc.Close(); err != nil {
		t.Fatalf("unrelated process blocked native drain: %v", err)
	}
	lifecycle, exists, err := execution.ReadRuntimeLifecycle(profile)
	if err != nil || !exists {
		t.Fatalf("missing native lifecycle: %v", err)
	}
	if lifecycle.SandboxPhase != "Stopped" || lifecycle.ActiveProcesses != 0 {
		t.Fatalf("native drain count/phase incorrect: %+v", lifecycle)
	}
}
