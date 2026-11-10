package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/testutil/openshell"
)

func TestPriorOpenShellAttemptIsIsolatedBeforeReplacement(t *testing.T) {
	fixture := openshell.Install(t)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	run := &agentRun{Engine: e, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: "code", Purpose: "primary", CWD: cwd, Execution: &execution.Profile{Launcher: "/tmp/bridges/openshell/launch.mjs"}}}
	prior := model.Attempt{AttemptID: "prior-open-shell", Task: task.ID, ProjectID: project.ID, PlanVersion: task.PlanVersion, Role: "code", Purpose: "primary", Status: "failed", Transport: "acp", ExecutionID: "attempt-456", ExecutionGeneration: 2, SandboxName: execution.OpenShellSandboxName("attempt-456"), SandboxID: "test-sandbox-" + execution.OpenShellSandboxName("attempt-456"), GatewayName: "whirlwind-test-local", GatewayEndpoint: "http://127.0.0.1:8080", Workspace: "whirlwind-test", ExecutionLabelValue: execution.OpenShellExecutionLabel("attempt-456", 2)}
	if err := e.Store.Put(context.Background(), "sessions", prior.AttemptID, prior, 0); err != nil {
		t.Fatal(err)
	}
	args := []string{"sandbox", "create", "--name", prior.SandboxName, "--from", "image", "--policy", "/tmp/policy", "--driver-config-json", "{}", "--label", "whirlwind.io/execution=" + prior.ExecutionLabelValue, "--detach", "--no-auto-providers", "--output", "json", "--gateway", prior.GatewayName, "--gateway-endpoint", prior.GatewayEndpoint, "--workspace", prior.Workspace, "--", "/bin/sh", "-c", "exec sleep infinity"}
	if _, err := fixture.RunCLI(t, args...); err != nil {
		t.Fatal(err)
	}
	if err := run.reconcileOpenShellAttempts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !hasRuntimeSandboxCall(fixture.Calls(t), "stop") || !hasRuntimeSandboxCall(fixture.Calls(t), "delete") {
		t.Fatalf("replacement was allowed without stopping and deleting prior sandbox: %v", fixture.Calls(t))
	}
}

func TestAgentAttemptReconcilesItsSandboxBeforeProviderReturn(t *testing.T) {
	fixture := openshell.Install(t)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	profile := execution.Profile{ExecutionID: "provider-return", ExecutionGeneration: 2, SandboxName: execution.OpenShellSandboxName("provider-return"), GatewayName: "whirlwind-test-local", GatewayEndpoint: "http://127.0.0.1:8080", Workspace: "whirlwind-test", ExecutionLabelValue: execution.OpenShellExecutionLabel("provider-return", 2)}
	args := []string{"sandbox", "create", "--name", profile.SandboxName, "--from", "image", "--policy", "/tmp/policy", "--driver-config-json", "{}", "--label", "whirlwind.io/execution=" + profile.ExecutionLabelValue, "--detach", "--no-auto-providers", "--output", "json", "--gateway", profile.GatewayName, "--gateway-endpoint", profile.GatewayEndpoint, "--workspace", profile.Workspace, "--", "/bin/sh", "-c", "exec sleep infinity"}
	if _, err := fixture.RunCLI(t, args...); err != nil {
		t.Fatal(err)
	}
	attempt := &agentAttempt{agentRun: &agentRun{input: provider.Input{Execution: &execution.Profile{Launcher: "/opt/whirlwind/bridges/openshell/launch.mjs", ExecutionID: profile.ExecutionID, ExecutionGeneration: profile.ExecutionGeneration, SandboxName: profile.SandboxName, SandboxID: "test-sandbox-" + profile.SandboxName, GatewayName: profile.GatewayName, GatewayEndpoint: profile.GatewayEndpoint, Workspace: profile.Workspace, ExecutionLabelValue: profile.ExecutionLabelValue}}}}
	if err := attempt.reconcileOwnedRuntime(); err != nil {
		t.Fatal(err)
	}
	if !hasRuntimeSandboxCall(fixture.Calls(t), "stop") || !hasRuntimeSandboxCall(fixture.Calls(t), "delete") {
		t.Fatalf("provider return did not isolate its sandbox before handoff: %v", fixture.Calls(t))
	}
}

func hasRuntimeSandboxCall(calls [][]string, operation string) bool {
	for _, call := range calls {
		if len(call) > 1 && call[0] == "sandbox" && call[1] == operation {
			return true
		}
	}
	return false
}

func TestLegacyUnpinnedACPRuntimeBlocksUnsafeReplacement(t *testing.T) {
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	run := &agentRun{Engine: e, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: "code", Purpose: "primary", CWD: cwd, Execution: &execution.Profile{Launcher: "/tmp/bridges/openshell/launch.mjs"}}}
	legacy := model.Attempt{AttemptID: "legacy-open-shell", Task: task.ID, ProjectID: project.ID, PlanVersion: task.PlanVersion, Role: "code", Purpose: "primary", Status: "interrupted", Transport: "acp"}
	if err := e.Store.Put(context.Background(), "sessions", legacy.AttemptID, legacy, 0); err != nil {
		t.Fatal(err)
	}
	if err := run.reconcileOpenShellAttempts(context.Background()); err == nil || !errors.Is(err, execution.ErrIsolationUnverified) {
		t.Fatalf("legacy unpinned ACP attempt was treated as isolated: %v", err)
	}
}

func TestInterruptedKnownLocalRuntimeResumesOnlyAfterCleanProcessReap(t *testing.T) {
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	run := &agentRun{Engine: e, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: "code", Purpose: "primary", CWD: cwd, Execution: &execution.Profile{Launcher: "/opt/whirlwind/bridges/openshell/launch.mjs"}}}
	legacy := model.Attempt{AttemptID: "legacy-local-clean", Task: task.ID, ProjectID: project.ID, PlanVersion: task.PlanVersion, Role: "code", Purpose: "primary", Status: "interrupted", Transport: "acp", RuntimeKind: "local", RuntimeLauncher: "/opt/whirlwind/bridges/sandbox/launch.mjs"}
	if err := e.Store.Put(context.Background(), "sessions", legacy.AttemptID, legacy, 0); err != nil {
		t.Fatal(err)
	}
	if err := run.reconcileOpenShellAttempts(context.Background()); err != nil {
		t.Fatalf("verified clean local legacy process tree remained blocked: %v", err)
	}
}

func TestRunningLocalAndUnknownExternalLegacyAttemptsRemainBlocked(t *testing.T) {
	tests := []struct {
		name    string
		attempt model.Attempt
	}{
		{name: "local still running", attempt: model.Attempt{RuntimeKind: "local", RuntimeLauncher: "/opt/whirlwind/bridges/sandbox/launch.mjs", Status: "running"}},
		{name: "unknown external", attempt: model.Attempt{RuntimeKind: "openshell", Status: "interrupted"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e, task, cwd := testEngine(t)
			project, err := e.Project(context.Background(), task.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			run := &agentRun{Engine: e, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: "code", Purpose: "primary", CWD: cwd, Execution: &execution.Profile{Launcher: "/opt/whirlwind/bridges/openshell/launch.mjs"}}}
			attempt := test.attempt
			attempt.AttemptID = "legacy-blocked"
			attempt.Task = task.ID
			attempt.ProjectID = task.ProjectID
			attempt.PlanVersion = task.PlanVersion
			attempt.Role = "code"
			attempt.Purpose = "primary"
			attempt.Transport = "acp"
			if err := e.Store.Put(context.Background(), "sessions", attempt.AttemptID, attempt, 0); err != nil {
				t.Fatal(err)
			}
			if err := run.reconcileOpenShellAttempts(context.Background()); err == nil || !errors.Is(err, execution.ErrIsolationUnverified) {
				t.Fatalf("unsafe legacy writer was treated as isolated: %v", err)
			}
		})
	}
}

func TestLegacyLocalProcessInspectionFailureBlocksRecovery(t *testing.T) {
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	e.Root = root
	run := &agentRun{Engine: e, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: "code", Purpose: "primary", CWD: cwd, Execution: &execution.Profile{Launcher: "/opt/whirlwind/bridges/openshell/launch.mjs"}}}
	legacy := model.Attempt{AttemptID: "legacy-local-inspection-error", Task: task.ID, ProjectID: task.ProjectID, PlanVersion: task.PlanVersion, Role: "code", Purpose: "primary", Status: "interrupted", Transport: "acp", RuntimeKind: "local", RuntimeLauncher: "/opt/whirlwind/bridges/sandbox/launch.mjs"}
	if err := e.Store.Put(context.Background(), "sessions", legacy.AttemptID, legacy, 0); err != nil {
		t.Fatal(err)
	}
	if err := run.reconcileOpenShellAttempts(context.Background()); err == nil || !errors.Is(err, execution.ErrIsolationUnverified) {
		t.Fatalf("unavailable process proof was treated as clean: %v", err)
	}
}

func TestRuntimeOwnershipUsesDurableOperationForTasklessRuns(t *testing.T) {
	run := &agentRun{project: model.Project{ID: "project"}, task: model.Task{PlanVersion: 1}, input: provider.Input{OperationID: "operation-a", Role: "ideate", Purpose: "primary", UnitID: "unit"}}
	other := model.Attempt{ProjectID: "project", PlanVersion: 1, OperationID: "operation-b", Role: "ideate", Purpose: "primary", UnitID: "unit"}
	same := other
	same.OperationID = "operation-a"
	if sameRecoveryWorkItem(other, run) {
		t.Fatal("unrelated taskless run was treated as the same runtime owner")
	}
	if !sameRecoveryWorkItem(same, run) {
		t.Fatal("matching durable taskless operation was not recognized")
	}
	run.input.OperationID = ""
	if sameRecoveryWorkItem(other, run) {
		t.Fatal("taskless recovery matched without a durable operation identity")
	}
}

// Persisted native attempts require a clean process registry, never a gateway.
func TestPriorNonoAttemptReconcilesLocalProcessOwnership(t *testing.T) {
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	run := &agentRun{Engine: e, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: "ideate", CWD: cwd, Execution: &execution.Profile{Launcher: "/app/bridges/nono/launch.mjs"}}}
	prior := model.Attempt{AttemptID: "prior-nono", ExecutionID: "prior-nono", ExecutionGeneration: 1, RuntimeKind: "nono", Task: task.ID, ProjectID: project.ID, PlanVersion: task.PlanVersion, Role: "ideate", Status: "interrupted", Transport: "acp"}
	if err := e.Store.Put(context.Background(), "sessions", prior.AttemptID, prior, 0); err != nil {
		t.Fatal(err)
	}
	if err := run.reconcileOpenShellAttempts(context.Background()); err != nil {
		t.Fatalf("native retry tried gateway reconciliation: %v", err)
	}
	kind, _ := attemptRuntimeKind(run.input.Execution)
	if kind != "nono" {
		t.Fatalf("native attempt mislabeled %q", kind)
	}
}
