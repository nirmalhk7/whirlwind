package execution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	standaloneFixtureSecondAttempt = "attempt-two"
)

func TestStandaloneRegistryKeepsRuntimeUntilVerifiedCleanup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "supervisor", "standalone")
	first := standaloneRegistryProfile(root, "attempt-one")
	second := standaloneRegistryProfile(root, standaloneFixtureSecondAttempt)
	requireExecutionSuccess(t, RegisterStandaloneRuntime(first))
	requireExecutionSuccess(t, RegisterStandaloneRuntime(second))
	entries, err := ReadStandaloneRuntimes(root)
	if err != nil || len(entries) != 2 || entries[0].ExecutionID != "attempt-one" || entries[1].ExecutionID != standaloneFixtureSecondAttempt {
		t.Fatalf("standalone registry entries: %+v %v", entries, err)
	}
	if entries[0].OwnerPID != os.Getpid() || entries[0].OwnerStarted == "" || entries[0].CWD == "" || entries[0].DeadlineAt == "" {
		t.Fatalf("registry omitted supervisor ownership or recovery bounds: %+v", entries[0])
	}
	data, err := os.ReadFile(StandaloneRegistryPath(root))
	requireExecutionSuccess(t, err)
	var fields map[string]any
	requireExecutionSuccess(t, json.Unmarshal(data, &fields))
	if strings.Contains(string(data), "secret") || fields["version"] != float64(standaloneRegistryVersion) {
		t.Fatalf("registry version/secret contract: %s", data)
	}
	info, err := os.Stat(StandaloneRegistryPath(root))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("registry mode is not private: %v %v", info, err)
	}
	requireExecutionSuccess(t, MarkStandaloneCleanupPending(first))
	entries, err = ReadStandaloneRuntimes(root)
	if err != nil || len(entries) != 2 || entries[0].State != "cleanup_pending" {
		t.Fatalf("failed cleanup locator was lost: %+v %v", entries, err)
	}
	requireExecutionSuccess(t, CompleteStandaloneRuntime(first))
	entries, err = ReadStandaloneRuntimes(root)
	if err != nil || len(entries) != 1 || entries[0].ExecutionID != standaloneFixtureSecondAttempt {
		t.Fatalf("completion removed unrelated locator: %+v %v", entries, err)
	}
}

func standaloneRegistryProfile(root, id string) Profile {
	return Profile{ExecutionID: id, ExecutionGeneration: 1, LifecycleFile: filepath.Join(root, id, "openshell.json"),
		SandboxName: OpenShellSandboxName(id), GatewayName: "local", GatewayEndpoint: "http://127.0.0.1:8080",
		Workspace: "workspace", Launcher: "/app/bridges/openshell/launch.mjs", SupervisorDir: root,
		CWD: filepath.Join(root, "worktree"), DeadlineAt: time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)}
}
