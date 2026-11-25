package execution

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/testutil/openshell"
)

const (
	launchFixtureAttempt       = "attempt-123"
	launchFixtureFailedAttempt = "attempt-failure"
)

func TestOpenShellLaunchUsesPinnedDetachedSandboxAndSeparateExec(t *testing.T) {
	fixture := openshell.Install(t)
	fixture.EnableCommandExecution()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	requireExecutionSuccess(t, err)
	worktree := filepath.Join(root, "worktree")
	requireExecutionSuccess(t, os.Mkdir(worktree, 0700))
	worktree, err = filepath.EvalSymlinks(worktree)
	requireExecutionSuccess(t, err)
	profile := Profile{
		ExecutionID: launchFixtureAttempt, ExecutionGeneration: 3,
		SandboxName: OpenShellSandboxName(launchFixtureAttempt), LifecycleFile: filepath.Join(root, "supervisor", "openshell.json"),
		GatewayName: "whirlwind-test-local", GatewayEndpoint: "http://127.0.0.1:8080", Workspace: "whirlwind-test",
		ExecutionLabelValue: OpenShellExecutionLabel(launchFixtureAttempt, 3), DeadlineAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		CWD: worktree, ReadOnly: true, ReadRoots: []string{}, Domains: []string{}, Environment: []string{},
	}
	profileData, err := json.Marshal(profile)
	requireExecutionSuccess(t, err)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "test-image")
	t.Setenv("WHIRLWIND_OPENSHELL_PROXY_TOKEN", strings.Repeat("a", 64))
	xdgConfig := filepath.Join(root, "host-config")
	requireExecutionSuccess(t, os.Mkdir(xdgConfig, 0700))
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	xdgState := filepath.Join(root, "host-state")
	requireExecutionSuccess(t, os.Mkdir(xdgState, 0700))
	t.Setenv("XDG_STATE_HOME", xdgState)
	command := exec.CommandContext(context.Background(), "node", filepath.Join("..", "..", "bridges", "openshell", "launch.mjs"), string(profileData), "--", "/usr/bin/true")
	command.Dir = filepath.Join("..", "..", "internal", "execution")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("OpenShell launcher failed: %v: %s", err, output)
	}
	assertDetachedOpenShellIdentity(t, fixture, profile)
	assertOpenShellBootstrapEnvironment(t, fixture, xdgConfig, xdgState)
}

func assertDetachedOpenShellIdentity(t *testing.T, fixture *openshell.Fixture, profile Profile) {
	t.Helper()
	if !fixture.HasInertInit(t) {
		t.Fatal("persistent sandbox was not created with the required inert init process")
	}
	calls := fixture.Calls(t)
	create := findOpenShellCall(calls, "create")
	if create == nil || argumentValue(create, "--name") != profile.SandboxName || !containsArgument(create, "--detach") || containsArgument(create, "--no-keep") || !containsArgument(create, "--no-auto-providers") || argumentValue(create, "--output") != "json" {
		t.Fatalf("sandbox create did not use persisted detached identity: %v", create)
	}
	for index := 0; index+1 < len(create); index++ {
		if create[index] == "--env" && strings.HasPrefix(create[index+1], "HOME=/sandbox/home") {
			t.Fatalf("sandbox create selected an unscoped runtime home: %v", create)
		}
	}
	if execCall := findOpenShellCall(calls, "exec"); execCall == nil {
		t.Fatalf("ACP command was not started by separate sandbox exec: %v", calls)
	}
	for _, call := range calls {
		if isSandboxLifecycleCall(call) && (argumentValue(call, "--gateway") != profile.GatewayName || argumentValue(call, "--gateway-endpoint") != profile.GatewayEndpoint || argumentValue(call, "--workspace") != profile.Workspace) {
			t.Fatalf("OpenShell lifecycle call escaped pinned authority: %v", call)
		}
	}
}

func assertOpenShellBootstrapEnvironment(t *testing.T, fixture *openshell.Fixture, xdgConfig, xdgState string) {
	t.Helper()
	environmentCalls := fixture.EnvironmentCalls(t)
	for _, operation := range []string{"create", "exec"} {
		found := slices.ContainsFunc(environmentCalls, func(call openshell.EnvironmentCall) bool {
			return call.Operation == operation && call.XDGPresent && call.XDGConfigHome == xdgConfig && call.XDGStatePresent && call.XDGStateHome == xdgState
		})
		if !found {
			t.Fatalf("host XDG gateway configuration was not preserved for sandbox %s: %+v", operation, environmentCalls)
		}
	}
	seeded := make(map[string]bool)
	for _, key := range fixture.EnvironmentSeedKeys(t) {
		seeded[key] = true
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if !seeded[key] {
			t.Errorf("sandbox exec environment bootstrap omitted %s", key)
		}
	}
	if seeded["WHIRLWIND_MCP_BROKER_TOKEN"] {
		t.Fatal("host-only MCP broker token crossed into sandbox environment")
	}
	if !fixture.RuntimeHomeScoped(t) {
		t.Fatal("guest runtime home was not scoped to the private per-execution temp root")
	}
}

func TestOpenShellLaunchPreservesCreateFailureWhenCleanupAlsoFails(t *testing.T) {
	fixture := openshell.Install(t)
	fixture.FailCreate(t)
	fixture.FailList(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(root, "worktree")
	if err := os.Mkdir(worktree, 0700); err != nil {
		t.Fatal(err)
	}
	worktree, err = filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	profile := Profile{
		ExecutionID: launchFixtureFailedAttempt, ExecutionGeneration: 1,
		SandboxName: OpenShellSandboxName(launchFixtureFailedAttempt), LifecycleFile: filepath.Join(root, "supervisor", "openshell.json"),
		GatewayName: "whirlwind-test-local", GatewayEndpoint: "http://127.0.0.1:8080", Workspace: "whirlwind-test",
		ExecutionLabelValue: OpenShellExecutionLabel(launchFixtureFailedAttempt, 1), DeadlineAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		CWD: worktree, ReadOnly: true, ReadRoots: []string{}, Domains: []string{}, Environment: []string{},
	}
	profileData, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "test-image")
	t.Setenv("WHIRLWIND_OPENSHELL_PROXY_TOKEN", strings.Repeat("b", 64))
	command := exec.Command("node", filepath.Join("..", "..", "bridges", "openshell", "launch.mjs"), string(profileData), "--", "/usr/bin/true")
	command.Dir = filepath.Join("..", "..", "internal", "execution")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("failing create and cleanup unexpectedly succeeded")
	}
	if !strings.Contains(string(output), "controlled create failure") || !strings.Contains(string(output), "controlled list failure") {
		t.Fatalf("primary create failure or cleanup failure was lost: %s", output)
	}
}

func findOpenShellCall(calls [][]string, operation string) []string {
	for _, call := range calls {
		if len(call) >= 2 && call[0] == "sandbox" && call[1] == operation {
			return call
		}
	}
	return nil
}

func argumentValue(args []string, name string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == name {
			return args[index+1]
		}
	}
	return ""
}

func containsArgument(args []string, expected string) bool {
	for _, arg := range args {
		if arg == expected {
			return true
		}
	}
	return false
}

func isSandboxLifecycleCall(args []string) bool {
	return len(args) >= 2 && args[0] == "sandbox"
}
