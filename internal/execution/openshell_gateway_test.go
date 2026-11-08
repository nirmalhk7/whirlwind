package execution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveOpenShellGatewayPinsActiveLocalGatewayAndWorkspace(t *testing.T) {
	fixture := `[{"name":"inactive","endpoint":"https://127.0.0.1:18080","active":false,"is_remote":false},{"name":"worker","endpoint":"https://127.0.0.1:19080","active":true,"is_remote":false}]`
	installGatewayCLI(t, fixture)
	t.Setenv("OPENSHELL_WORKSPACE", "plan-workspace")

	name, endpoint, workspace, err := ResolveOpenShellGateway(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if name != "worker" || endpoint != "https://127.0.0.1:19080" || workspace != "plan-workspace" {
		t.Fatalf("resolved gateway identity = %q %q %q", name, endpoint, workspace)
	}
}

func TestPinOpenShellRuntimeCreatesExternalDeterministicIdentity(t *testing.T) {
	installGatewayCLI(t, `[{"name":"worker","endpoint":"https://127.0.0.1:19080","active":true,"is_remote":false}]`)
	t.Setenv("OPENSHELL_WORKSPACE", "plan-workspace")
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	runtimeDir := filepath.Join(root, "runtime")
	if err := os.MkdirAll(worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	profile := Profile{CWD: worktree, RuntimeDir: runtimeDir, ReadRoots: []string{worktree}}
	deadline := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)
	first, err := PinOpenShellRuntime(context.Background(), profile, "attempt-123", 4, deadline, filepath.Join(root, "supervisor"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := PinOpenShellRuntime(context.Background(), profile, "attempt-123", 4, deadline, filepath.Join(root, "supervisor"))
	if err != nil {
		t.Fatal(err)
	}
	if first.SandboxName == "" || first.SandboxName != second.SandboxName || first.LifecycleFile != second.LifecycleFile {
		t.Fatalf("attempt identity was not deterministic: first=%+v second=%+v", first, second)
	}
	if first.ExecutionID != "attempt-123" || first.ExecutionGeneration != 4 || first.GatewayName != "worker" || first.GatewayEndpoint != "https://127.0.0.1:19080" || first.Workspace != "plan-workspace" || first.DeadlineAt != deadline {
		t.Fatalf("pinned runtime identity lost its execution or authority: %+v", first)
	}
	if withinPath(first.LifecycleFile, worktree) || withinPath(first.LifecycleFile, runtimeDir) {
		t.Fatalf("trusted lifecycle state is inside a sandbox mount: %s", first.LifecycleFile)
	}
	if profile.Digest() != first.Digest() {
		t.Fatal("per-attempt runtime identity changed the reusable execution configuration digest")
	}
}

func TestPinPersistsExactRuntimeIdentityAndDeadlineBeforeLaunch(t *testing.T) {
	installGatewayCLI(t, `[{"name":"worker","endpoint":"https://127.0.0.1:19080","active":true,"is_remote":false}]`)
	t.Setenv("OPENSHELL_WORKSPACE", "plan-workspace")
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	if err := os.MkdirAll(worktree, 0700); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)
	profile, err := PinOpenShellRuntime(context.Background(), Profile{CWD: worktree}, "check-run-1", 1, deadline, filepath.Join(root, "supervisor", "executions"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(profile.LifecycleFile)
	if err != nil {
		t.Fatalf("runtime identity was not durable before launch: %v", err)
	}
	var identity SandboxLifecycle
	if err := json.Unmarshal(data, &identity); err != nil {
		t.Fatal(err)
	}
	if identity.ExecutionID != profile.ExecutionID || identity.ExecutionGeneration != profile.ExecutionGeneration || identity.SandboxName != profile.SandboxName || identity.GatewayName != profile.GatewayName || identity.GatewayEndpoint != profile.GatewayEndpoint || identity.Workspace != profile.Workspace || identity.ExecutionLabelValue != profile.ExecutionLabelValue || identity.DeadlineAt != profile.DeadlineAt || identity.SandboxPhase != "provisioning" || identity.SandboxHealth != "unknown" {
		t.Fatalf("prelaunch identity did not preserve the exact execution authority: %+v", identity)
	}
	info, err := os.Stat(profile.LifecycleFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("runtime identity file permissions are not private: %v %v", info, err)
	}
}

func TestResolveOpenShellGatewayRejectsUnrecognizedOrRemoteAuthority(t *testing.T) {
	for _, fixture := range []string{
		`[{"name":"worker","endpoint":"https://127.0.0.1:19080","active":true,"is_remote":true}]`,
		`[{"name":"worker","endpoint":"https://gateway.example","active":true,"is_remote":false}]`,
		`[{"name":"worker","endpoint":"https://127.0.0.1:19080","active":true,"is_remote":false},{"name":"other","endpoint":"https://127.0.0.1:19081","active":true,"is_remote":false}]`,
	} {
		installGatewayCLI(t, fixture)
		if _, _, _, err := ResolveOpenShellGateway(context.Background()); err == nil {
			t.Fatalf("accepted unsafe or ambiguous gateway metadata: %s", fixture)
		}
	}
}

func TestResolveOpenShellGatewayHonorsExplicitNonActiveSelection(t *testing.T) {
	installGatewayCLI(t, `[{"name":"inactive","endpoint":"https://127.0.0.1:18080","active":false,"is_remote":false},{"name":"worker","endpoint":"https://127.0.0.1:19080","active":true,"is_remote":false}]`)
	t.Setenv("OPENSHELL_GATEWAY", "inactive")
	t.Setenv("OPENSHELL_GATEWAY_ENDPOINT", "https://127.0.0.1:18080")
	name, endpoint, _, err := ResolveOpenShellGateway(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if name != "inactive" || endpoint != "https://127.0.0.1:18080" {
		t.Fatalf("explicit non-active gateway override ignored: name=%q endpoint=%q", name, endpoint)
	}
}

func TestResolveOpenShellGatewayRejectsUnknownExplicitSelection(t *testing.T) {
	installGatewayCLI(t, `[{"name":"worker","endpoint":"https://127.0.0.1:19080","active":true,"is_remote":false}]`)
	t.Setenv("OPENSHELL_GATEWAY", "missing")
	if _, _, _, err := ResolveOpenShellGateway(context.Background()); err == nil {
		t.Fatal("unknown explicit OpenShell gateway silently fell back to active gateway")
	}
}

func TestOpenShellSandboxNameFitsGatewayLimitAndKeepsDeterministicEntropy(t *testing.T) {
	name := OpenShellSandboxName("attempt-123")
	if len(name) > 19 {
		t.Fatalf("sandbox name %q exceeds v0.1.7 limit of 19 characters", name)
	}
	if name != OpenShellSandboxName("attempt-123") || name == OpenShellSandboxName("attempt-124") {
		t.Fatalf("sandbox name is not stable and execution-specific: %q", name)
	}
}

func installGatewayCLI(t *testing.T, fixture string) {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "gateways.json")
	if err := os.WriteFile(data, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(dir, "openshell")
	script := "#!/bin/sh\nprintf '%s' \"$(cat \"$FIXTURE_GATEWAYS\")\"\n"
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FIXTURE_GATEWAYS", data)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", cli)
}

func withinPath(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && relative[:min(2, len(relative))] != ".."
}
