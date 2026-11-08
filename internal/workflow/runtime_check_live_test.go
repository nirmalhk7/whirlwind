package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

const liveOwnedCheckVariable = "WHIRLWIND_OPENSHELL_LIVE_ACCEPTANCE"

func TestEngineOwnedCheckOpenShellAcceptance(t *testing.T) {
	if os.Getenv(liveOwnedCheckVariable) != "1" {
		t.Skipf("set %s=1 with a disposable healthy OpenShell gateway and Linux fake ACP image", liveOwnedCheckVariable)
	}
	cli := requireLiveOwnedCheckSetup(t)
	privateHome := t.TempDir()
	t.Setenv("HOME", privateHome)
	t.Setenv("WHIRLWIND_OPENSHELL_PROXY_TOKEN", liveOwnedCheckToken(t))

	engine, task, cwd := testEngine(t)
	project, err := engine.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(cwd, "source.txt")
	if err := os.WriteFile(source, []byte("original source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	profile, err := execution.ForPolicy(cwd, true, project.PolicyHash, project.Policy.Sandbox)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ExecutionID != "" || profile.SandboxName != "" || profile.GatewayName != "" {
		t.Fatalf("ForPolicy should be unpinned before the normal Engine-owned check path: %+v", profile)
	}

	check := model.Check{
		Name:           "live-owned-output-and-readonly-boundary",
		OutputPaths:    []string{"dist"},
		TimeoutSeconds: 120,
		Command: []string{"/bin/sh", "-c", `set -eu
test "$(cat source.txt)" = "original source"
mkdir -p dist
printf 'approved artifact\n' > dist/result.txt
if printf 'changed source\n' > source.txt 2>/dev/null; then
	printf 'source-write-was-allowed\n'
	exit 31
else
	printf 'source-write-denied\n'
fi
if printf 'escaped\n' > ../outside-check.txt 2>/dev/null; then
	printf 'outside-write-was-allowed\n'
	exit 32
else
	printf 'outside-write-denied\n'
fi
printf 'declared-output-write-succeeded\npayload-completed\n'`},
	}
	checkCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := engine.runOwnedCheck(checkCtx, cwd, check, profile, runtimeCheckOwner{TaskID: task.ID, Role: "verify"})
	if err != nil {
		t.Fatalf("Engine.runOwnedCheck failed: %v; output=%s", err, result.Output)
	}
	if !result.Passed || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("OpenShell payload did not pass: %+v", result)
	}
	for _, marker := range []string{"source-write-denied", "outside-write-denied", "declared-output-write-succeeded", "payload-completed"} {
		if !strings.Contains(result.Output, marker) {
			t.Fatalf("running payload omitted enforcement marker %q: %s", marker, result.Output)
		}
	}
	if !result.Runtime.CleanupVerified || result.Runtime.Kind != "openshell" || result.Runtime.ExecutionID == "" || result.Runtime.Generation < 1 || result.Runtime.SandboxID == "" {
		t.Fatalf("Engine result lacks verified OpenShell execution authority: %+v", result.Runtime)
	}
	artifact, err := os.ReadFile(filepath.Join(cwd, "dist", "result.txt"))
	if err != nil || string(artifact) != "approved artifact\n" {
		t.Fatalf("declared output write was not preserved on the host: %q, %v", artifact, err)
	}
	unchanged, err := os.ReadFile(source)
	if err != nil || string(unchanged) != "original source\n" {
		t.Fatalf("read-only source changed on host: %q, %v", unchanged, err)
	}
	outside := filepath.Join(filepath.Dir(cwd), "outside-check.txt")
	if _, err := os.Lstat(outside); !os.IsNotExist(err) {
		t.Fatalf("payload created a host path outside the worktree: %s (%v)", outside, err)
	}

	assertPersistedOwnedCheck(t, engine, task, cwd, check, result.Runtime)
	assertOwnedSandboxAbsent(t, cli, result.Runtime)
}

func requireLiveOwnedCheckSetup(t *testing.T) string {
	t.Helper()
	cli := os.Getenv("WHIRLWIND_OPENSHELL_CLI")
	if cli == "" {
		t.Fatal("WHIRLWIND_OPENSHELL_LIVE_ACCEPTANCE=1 requires WHIRLWIND_OPENSHELL_CLI")
	}
	if info, err := os.Stat(cli); err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		t.Fatalf("configured OpenShell CLI is unavailable or not executable: %v", err)
	}
	for _, name := range []string{"WHIRLWIND_OPENSHELL_IMAGE", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "OPENSHELL_GATEWAY", "OPENSHELL_GATEWAY_ENDPOINT", "OPENSHELL_WORKSPACE"} {
		if os.Getenv(name) == "" {
			t.Fatalf("WHIRLWIND_OPENSHELL_LIVE_ACCEPTANCE=1 requires %s", name)
		}
	}
	return cli
}

func liveOwnedCheckToken(t *testing.T) string {
	t.Helper()
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(token[:])
}

func assertPersistedOwnedCheck(t *testing.T, engine *Engine, task model.Task, cwd string, check model.Check, identity verification.RuntimeIdentity) {
	t.Helper()
	rows, err := engine.Store.List(context.Background(), "runtime_jobs")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != identity.ExecutionID {
		t.Fatalf("owned check should persist one unique execution job keyed by its result identity; rows=%+v identity=%+v", rows, identity)
	}
	job, err := store.Decode[runtimeCheckJob](rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if job.Stage != "complete" || !job.CleanupVerified || job.TaskID != task.ID || job.Role != "verify" || job.CheckID != check.Name ||
		job.ExecutionID != identity.ExecutionID || job.ExecutionGeneration != identity.Generation || job.SandboxName != identity.SandboxName || job.SandboxID != identity.SandboxID ||
		job.GatewayName != identity.GatewayName || job.GatewayEndpoint != identity.GatewayEndpoint || job.Workspace != identity.Workspace || job.LifecycleFile != identity.LifecycleFile {
		t.Fatalf("persisted runtime job does not authorize the exact cleaned result: job=%+v result=%+v", job, identity)
	}
	if withinRuntimePath(job.LifecycleFile, cwd) {
		t.Fatalf("runtime lifecycle proof must remain outside the checked worktree: %s", job.LifecycleFile)
	}
}

func assertOwnedSandboxAbsent(t *testing.T, cli string, identity verification.RuntimeIdentity) {
	t.Helper()
	command := exec.Command(cli, "sandbox", "list", "--output", "json", "--gateway", identity.GatewayName,
		"--gateway-endpoint", identity.GatewayEndpoint, "--workspace", identity.Workspace)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
		"XDG_CONFIG_HOME=" + os.Getenv("XDG_CONFIG_HOME"), "XDG_STATE_HOME=" + os.Getenv("XDG_STATE_HOME")}
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("OpenShell sandbox list failed with exit %d", exit.ExitCode())
		}
		t.Fatalf("OpenShell sandbox list failed: %v", err)
	}
	var response struct {
		Sandboxes []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Workspace string `json:"workspace"`
			Phase     string `json:"phase"`
		} `json:"sandboxes"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatalf("OpenShell sandbox list returned invalid JSON: %v", err)
	}
	for _, sandbox := range response.Sandboxes {
		if sandbox.ID == identity.SandboxID || sandbox.Name == identity.SandboxName {
			t.Fatalf("owned sandbox remains after runOwnedCheck cleanup: id=%s name=%s phase=%s workspace=%s", sandbox.ID, sandbox.Name, sandbox.Phase, sandbox.Workspace)
		}
	}
}
