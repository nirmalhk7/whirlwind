package workflow

import (
	"context"
	"testing"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// Native verification must retain durable proof, rather than bypassing check ownership.
func TestOwnedNonoCheckRetainsVerifiedRuntimeProof(t *testing.T) {
	e, task, cwd := testEngine(t)
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", "")
	profile, err := execution.ForPolicy(cwd, true, "nono-policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.runOwnedCheck(context.Background(), cwd, model.Check{Name: "native-proof", Command: []string{"/usr/bin/true"}}, profile, runtimeCheckOwner{TaskID: task.ID, Role: "verify"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.Runtime.Kind != "nono" || !result.Runtime.CleanupVerified {
		t.Fatalf("native check lost cleanup proof: %+v", result)
	}
	record, err := e.Store.Get(context.Background(), "runtime_jobs", result.Runtime.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[runtimeCheckJob](record)
	if err != nil {
		t.Fatal(err)
	}
	if job.Stage != "complete" || !job.CleanupVerified || job.SandboxID != result.Runtime.SandboxID {
		t.Fatalf("native runtime job not completed: %+v", job)
	}
	if !runtimeIdentityComplete(result.Runtime) {
		t.Fatalf("native result rejected by review proof: %+v", result.Runtime)
	}
}
