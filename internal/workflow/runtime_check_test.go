package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
)

func TestOwnedCheckPersistsIdentityAndReturnsAfterVerifiedCleanup(t *testing.T) {
	fixture := openshell.Install(t)
	fixture.EnableCommandExecution()
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "test-image")
	t.Setenv("WHIRLWIND_OPENSHELL_PROXY_TOKEN", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := execution.ForPolicy(cwd, true, project.PolicyHash, project.Policy.Sandbox)
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.runOwnedCheck(context.Background(), cwd, model.Check{Name: "runtime-check", Command: []string{"/usr/bin/true"}}, profile, runtimeCheckOwner{TaskID: task.ID, Role: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("OpenShell check failed: %+v", result)
	}
	if !result.Runtime.CleanupVerified || result.Runtime.ExecutionID == "" || result.Runtime.SandboxID == "" || result.Runtime.LifecycleFile == "" {
		t.Fatalf("check returned without durable identity and cleanup proof: %+v", result.Runtime)
	}
	if withinRuntimePath(result.Runtime.LifecycleFile, cwd) {
		t.Fatalf("lifecycle proof was placed in the sandboxed worktree: %s", result.Runtime.LifecycleFile)
	}
	job, err := e.Store.Get(context.Background(), "runtime_jobs", result.Runtime.ExecutionID)
	if err != nil {
		t.Fatalf("check job locator was not persisted: %v", err)
	}
	stored, err := store.Decode[struct {
		Stage     string `json:"stage"`
		Lifecycle string `json:"lifecycle_file"`
		SandboxID string `json:"sandbox_id"`
		CleanupOK bool   `json:"cleanup_verified"`
	}](job)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Stage != "complete" || stored.Lifecycle != result.Runtime.LifecycleFile || stored.SandboxID != result.Runtime.SandboxID || !stored.CleanupOK {
		t.Fatalf("check job did not retain the exact completed runtime: %+v", stored)
	}
	if _, err := os.Stat(filepath.Dir(result.Runtime.LifecycleFile)); err != nil {
		t.Fatalf("runtime lifecycle directory was removed: %v", err)
	}
	assertOpenShellCleanupCalls(t, fixture.Calls(t), result.Runtime.SandboxName)
}

func TestOwnedCheckLiveOpenShellKeepsSourceReadOnlyAndGrantsOnlyDeclaredOutput(t *testing.T) {
	if os.Getenv("WHIRLWIND_RUN_LIVE_OPENSHELL") != "1" || os.Getenv("WHIRLWIND_OPENSHELL_IMAGE") == "" {
		t.Skip("set WHIRLWIND_RUN_LIVE_OPENSHELL=1 and WHIRLWIND_OPENSHELL_IMAGE for the real OpenShell boundary acceptance")
	}
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(cwd, "source.txt")
	if err := os.WriteFile(source, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	profile, err := execution.ForPolicy(cwd, true, project.PolicyHash, project.Policy.Sandbox)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		check model.Check
		pass  bool
	}{
		{check: model.Check{Name: "declared-output", OutputPaths: []string{"dist"}, Command: []string{"/bin/sh", "-c", "mkdir -p dist && printf artifact > dist/result"}}, pass: true},
		{check: model.Check{Name: "source-write-denied", Command: []string{"/bin/sh", "-c", "printf changed > source.txt"}}},
		{check: model.Check{Name: "outside-write-denied", Command: []string{"/bin/sh", "-c", "printf escaped > ../outside.txt"}}},
	}
	for _, test := range checks {
		result, err := e.runOwnedCheck(context.Background(), cwd, test.check, profile, runtimeCheckOwner{TaskID: task.ID, Role: "verify"})
		if err != nil {
			t.Fatalf("%s did not complete runtime reconciliation: %v", test.check.Name, err)
		}
		if result.Passed != test.pass || !result.Runtime.CleanupVerified {
			t.Fatalf("%s passed=%t cleanup=%t, want pass=%t with cleanup proof: %+v", test.check.Name, result.Passed, result.Runtime.CleanupVerified, test.pass, result)
		}
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "original" {
		t.Fatalf("OpenShell check changed read-only source: %q err=%v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(cwd, "dist", "result")); err != nil || string(data) != "artifact" {
		t.Fatalf("declared output directory was not writable: %q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cwd), "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("check wrote outside its worktree: %v", err)
	}
}

func TestRuntimeCheckDeadlinePreservesConfiguredLimitAndParentDeadline(t *testing.T) {
	check := model.Check{Name: "long-check", TimeoutSeconds: 600}
	if got := runtimeCheckDeadline(context.Background(), check); got != 600*time.Second {
		t.Fatalf("configured check timeout = %s, want 10 minutes", got)
	}
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Minute))
	defer cancel()
	if got := runtimeCheckDeadline(parent, check); got > 2*time.Minute || got <= 0 {
		t.Fatalf("parent deadline was not applied to check runtime: %s", got)
	}
}

func TestInterruptedCheckIsReconciledAndPausesBeforeReplacement(t *testing.T) {
	fixture := openshell.Install(t)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	e, task, cwd := testEngine(t)
	const id = "crashed-check-run"
	const generation = 2
	deadline := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)
	name := execution.OpenShellSandboxName(id)
	label := execution.OpenShellExecutionLabel(id, generation)
	endpoint, workspace, gateway := "http://127.0.0.1:8080", "whirlwind-test", "whirlwind-test-local"
	created, err := fixture.RunCLI(t, "sandbox", "create", "--name", name, "--from", "image", "--policy", "/tmp/policy", "--driver-config-json", "{}", "--label", "whirlwind.io/execution="+label, "--detach", "--no-auto-providers", "--output", "json", "--gateway", gateway, "--gateway-endpoint", endpoint, "--workspace", workspace, "--", "/bin/sh", "-c", "exec sleep infinity")
	if err != nil {
		t.Fatal(err)
	}
	var sandbox struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created, &sandbox); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(e.Root, "supervisor", "executions")
	lifecycleFile := filepath.Join(root, id, "openshell.json")
	if err := os.MkdirAll(filepath.Dir(lifecycleFile), 0700); err != nil {
		t.Fatal(err)
	}
	proof, err := json.Marshal(execution.SandboxLifecycle{UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano), ExecutionID: id, ExecutionGeneration: generation, ExecutionLabelValue: label, DeadlineAt: deadline, SandboxName: name, SandboxID: sandbox.ID, GatewayName: gateway, GatewayEndpoint: endpoint, Workspace: workspace, SandboxPhase: "Ready", SandboxHealth: "healthy"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lifecycleFile, proof, 0600); err != nil {
		t.Fatal(err)
	}
	job := runtimeCheckJob{Stage: "running", ExecutionID: id, ExecutionGeneration: generation, ExecutionLabelValue: label, DeadlineAt: deadline, SupervisorDir: root, LifecycleFile: lifecycleFile, SandboxName: name, TaskID: task.ID, Role: "verify", CheckID: "test", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := e.Store.Put(context.Background(), "runtime_jobs", id, job, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.reconcileRuntimeJobs(context.Background(), runtimeCheckOwner{}); !errors.Is(err, ErrRuntimeCheckOutcomeUncertain) {
		t.Fatalf("supervisor restart did not reconcile interrupted check: %v", err)
	}
	profile, err := execution.ForPolicy(cwd, true, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.runOwnedCheck(context.Background(), cwd, model.Check{Name: "test", Command: []string{"/usr/bin/true"}}, profile, runtimeCheckOwner{TaskID: task.ID, Role: "verify"}); !errors.Is(err, ErrRuntimeCheckOutcomeUncertain) {
		t.Fatalf("replacement check did not stop for uncertain prior effects: %v", err)
	}
	storedTask, err := e.Store.Get(context.Background(), "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := store.Decode[model.Task](storedTask)
	if err != nil || paused.Stage != "paused" || paused.Error != ErrRuntimeCheckOutcomeUncertain.Error() {
		t.Fatalf("task did not preserve the uncertain check for human reconciliation: stage=%q error=%q decode=%v", paused.Stage, paused.Error, err)
	}
	storedJob, err := e.Store.Get(context.Background(), "runtime_jobs", id)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := store.Decode[runtimeCheckJob](storedJob)
	if err != nil || recovered.Stage != "uncertain" || !recovered.CleanupVerified {
		t.Fatalf("interrupted job outcome was not retained after cleanup: %+v err=%v", recovered, err)
	}
	assertOpenShellCleanupCalls(t, fixture.Calls(t), name)
}

func withinRuntimePath(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func assertOpenShellCleanupCalls(t *testing.T, calls [][]string, sandboxName string) {
	t.Helper()
	seen := map[string]bool{}
	for _, call := range calls {
		if len(call) >= 3 && call[0] == "sandbox" && (call[1] == "stop" || call[1] == "delete") && call[2] == sandboxName {
			seen[call[1]] = true
		}
	}
	if !seen["stop"] || !seen["delete"] {
		t.Fatalf("check returned without stopping and deleting its exact sandbox: calls=%v", calls)
	}
}
