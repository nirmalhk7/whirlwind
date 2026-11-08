package execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whirlwind/internal/testutil/openshell"
)

func TestReconcileOpenShellRuntimeStopsAndDeletesPersistedResource(t *testing.T) {
	fixture := openshell.Install(t)
	profile := openShellFixtureProfile(t)
	createOpenShellFixtureSandbox(t, fixture, profile)
	if err := ReconcileOpenShellRuntime(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	calls := fixture.Calls(t)
	if !hasSandboxOperation(calls, "stop") || !hasSandboxOperation(calls, "delete") || !hasSandboxOperation(calls, "list") {
		t.Fatalf("recovery did not stop, delete, and reconcile the same sandbox: %v", calls)
	}
}

func TestReconcileOpenShellRuntimeRejectsForeignResourceBeforeStop(t *testing.T) {
	fixture := openshell.Install(t)
	profile := openShellFixtureProfile(t)
	createOpenShellFixtureSandbox(t, fixture, profile)
	profile.SandboxID = "foreign-resource"
	if err := ReconcileOpenShellRuntime(context.Background(), profile); !errors.Is(err, ErrIsolationUnverified) {
		t.Fatalf("foreign sandbox identity was not rejected: %v", err)
	}
	if hasSandboxOperation(fixture.Calls(t), "stop") {
		t.Fatal("recovery stopped a resource with a different persisted ID")
	}
}

func TestReconcilePinsDiscoveredResourceIDBeforeStopping(t *testing.T) {
	fixture := openshell.Install(t)
	profile := openShellFixtureProfile(t)
	profile.SupervisorDir = filepath.Join(t.TempDir(), "supervisor", "standalone")
	profile.LifecycleFile = filepath.Join(profile.SupervisorDir, profile.ExecutionID, "openshell.json")
	profile.CWD = filepath.Join(t.TempDir(), "worktree")
	profile.DeadlineAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	profile.StandaloneRuntime = true
	createOpenShellFixtureSandbox(t, fixture, profile)
	if err := RegisterStandaloneRuntime(profile); err != nil {
		t.Fatal(err)
	}
	wrapChangingID(t, fixture, "replacement-resource")

	if err := ReconcileOpenShellRuntime(context.Background(), profile); !errors.Is(err, ErrIsolationUnverified) {
		t.Fatalf("resource ID change after initial discovery was accepted: %v", err)
	}
	if !hasSandboxOperation(fixture.Calls(t), "stop") || hasSandboxOperation(fixture.Calls(t), "delete") {
		t.Fatalf("reconciliation did not stop at the changed resource identity: %v", fixture.Calls(t))
	}
	data, err := os.ReadFile(StandaloneRegistryPath(profile.SupervisorDir))
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Executions []map[string]any `json:"executions"`
	}
	if err := json.Unmarshal(data, &registry); err != nil || len(registry.Executions) != 1 || registry.Executions[0]["sandbox_id"] != "test-sandbox-"+profile.SandboxName {
		t.Fatalf("discovered resource ID was not retained as durable recovery authority: %s %v", data, err)
	}
}

func wrapChangingID(t *testing.T, fixture *openshell.Fixture, changedID string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openshell-wrapper")
	script := `#!/bin/sh
output=$("$WHIRLWIND_FAKE_OPENSHELL" "$@") || exit $?
if [ "$1" = sandbox ] && [ "$2" = get ]; then
  printf '%s\n' "$output" | sed 's/"id":"test-sandbox-[^"]*"/"id":"` + changedID + `"/'
else
  printf '%s\n' "$output"
fi
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WHIRLWIND_FAKE_OPENSHELL", fixture.Binary)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", path)
}

func openShellFixtureProfile(t *testing.T) Profile {
	t.Helper()
	root := t.TempDir()
	lifecycle := filepath.Join(root, "supervisor", "openshell.json")
	profile := Profile{ExecutionID: "attempt-456", ExecutionGeneration: 2, SandboxName: OpenShellSandboxName("attempt-456"), LifecycleFile: lifecycle, GatewayName: "whirlwind-test-local", GatewayEndpoint: "http://127.0.0.1:8080", Workspace: "whirlwind-test", ExecutionLabelValue: OpenShellExecutionLabel("attempt-456", 2)}
	return profile
}

func createOpenShellFixtureSandbox(t *testing.T, fixture *openshell.Fixture, profile Profile) {
	t.Helper()
	args := []string{"sandbox", "create", "--name", profile.SandboxName, "--from", "image", "--policy", "/tmp/policy", "--driver-config-json", "{}", "--label", "whirlwind.io/execution=" + profile.ExecutionLabelValue, "--detach", "--no-auto-providers", "--gateway", profile.GatewayName, "--gateway-endpoint", profile.GatewayEndpoint, "--workspace", profile.Workspace, "--", "/bin/sh", "-c", "exec sleep infinity"}
	if _, err := fixture.RunCLI(t, args...); err != nil {
		t.Fatalf("seed OpenShell test resource: %v", err)
	}
}

func hasSandboxOperation(calls [][]string, operation string) bool {
	for _, call := range calls {
		if len(call) >= 2 && call[0] == "sandbox" && call[1] == operation {
			return true
		}
	}
	return false
}
