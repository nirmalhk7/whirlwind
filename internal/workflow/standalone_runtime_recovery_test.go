package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/testutil/openshell"
)

func TestReconcileRemovesStaleRegisteredStandaloneRuntime(t *testing.T) {
	fixture := openshell.Install(t)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	e, _, cwd := testEngine(t)
	profile := standaloneRecoveryProfile(e, cwd, "stale-probe")
	createStandaloneRecoverySandbox(t, fixture, profile)
	if err := execution.RegisterStandaloneRuntime(profile); err != nil {
		t.Fatal(err)
	}
	markStandaloneOwnerStale(t, profile, os.Getpid(), "reused-process-start")
	writeStandaloneLifecycle(t, profile)

	stopEngineOnStartup(t, e)

	entries, err := execution.ReadStandaloneRuntimes(profile.SupervisorDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("stale registered runtime was not removed after verified sandbox cleanup: %+v", entries)
	}
	if !hasRuntimeSandboxCall(fixture.Calls(t), "stop") || !hasRuntimeSandboxCall(fixture.Calls(t), "delete") {
		t.Fatalf("stale runtime was removed without exact sandbox stop/delete: %v", fixture.Calls(t))
	}
}

func TestReconcileKeepsCurrentUnexpiredStandaloneProbe(t *testing.T) {
	fixture := openshell.Install(t)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	e, _, cwd := testEngine(t)
	profile := standaloneRecoveryProfile(e, cwd, "current-probe")
	createStandaloneRecoverySandbox(t, fixture, profile)
	if err := execution.RegisterStandaloneRuntime(profile); err != nil {
		t.Fatal(err)
	}
	writeStandaloneLifecycle(t, profile)

	e.Reconcile(context.Background())

	entries, err := execution.ReadStandaloneRuntimes(profile.SupervisorDir)
	if err != nil || len(entries) != 1 || entries[0].ExecutionID != profile.ExecutionID {
		t.Fatalf("healthy current-supervisor probe was reaped: %+v %v", entries, err)
	}
	if hasRuntimeSandboxCall(fixture.Calls(t), "stop") || hasRuntimeSandboxCall(fixture.Calls(t), "delete") {
		t.Fatalf("healthy current-supervisor probe was stopped: %v", fixture.Calls(t))
	}
}

func TestReconcileCleansExpiredCurrentStandaloneProbe(t *testing.T) {
	fixture := openshell.Install(t)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	e, _, cwd := testEngine(t)
	profile := standaloneRecoveryProfile(e, cwd, "expired-probe")
	profile.DeadlineAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	createStandaloneRecoverySandbox(t, fixture, profile)
	if err := execution.RegisterStandaloneRuntime(profile); err != nil {
		t.Fatal(err)
	}
	writeStandaloneLifecycle(t, profile)

	e.Reconcile(context.Background())

	entries, err := execution.ReadStandaloneRuntimes(profile.SupervisorDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("expired probe remained registered after isolation: %+v %v", entries, err)
	}
	if !hasRuntimeSandboxCall(fixture.Calls(t), "stop") || !hasRuntimeSandboxCall(fixture.Calls(t), "delete") {
		t.Fatalf("expired probe was removed without sandbox cleanup: %v", fixture.Calls(t))
	}
}

func TestStepBlocksWhenStandaloneOwnerIsUnknown(t *testing.T) {
	fixture := openshell.Install(t)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	e, task, cwd := testEngine(t)
	profile := standaloneRecoveryProfile(e, cwd, "unknown-owner")
	createStandaloneRecoverySandbox(t, fixture, profile)
	if err := execution.RegisterStandaloneRuntime(profile); err != nil {
		t.Fatal(err)
	}
	markStandaloneOwnerStale(t, profile, 0, "")
	writeStandaloneLifecycle(t, profile)

	err := e.Step(context.Background(), task.ID)
	if !errors.Is(err, execution.ErrIsolationUnverified) {
		t.Fatalf("replacement task was not fenced by unknown runtime ownership: %v", err)
	}
	entries, err := execution.ReadStandaloneRuntimes(profile.SupervisorDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("unknown runtime evidence was discarded: %+v %v", entries, err)
	}
	if hasRuntimeSandboxCall(fixture.Calls(t), "stop") || hasRuntimeSandboxCall(fixture.Calls(t), "delete") {
		t.Fatalf("unknown runtime authority was acted on: %v", fixture.Calls(t))
	}
}

func TestStepRemainsBlockedWhenStandaloneReconciliationIsUncertain(t *testing.T) {
	fixture := openshell.Install(t)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	e, task, cwd := testEngine(t)
	profile := standaloneRecoveryProfile(e, cwd, "uncertain-owner")
	createStandaloneRecoverySandbox(t, fixture, profile)
	if err := execution.RegisterStandaloneRuntime(profile); err != nil {
		t.Fatal(err)
	}
	markStandaloneOwnerStale(t, profile, os.Getpid(), "reused-process-start")
	writeStandaloneLifecycle(t, profile)
	fixture.FailList(t)

	err := e.Step(context.Background(), task.ID)
	if !errors.Is(err, execution.ErrIsolationUnverified) {
		t.Fatalf("replacement task continued without isolation proof: %v", err)
	}
	entries, err := execution.ReadStandaloneRuntimes(profile.SupervisorDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("uncertain runtime locator was discarded: %+v %v", entries, err)
	}
}

func standaloneRecoveryProfile(e *Engine, cwd, id string) execution.Profile {
	root := filepath.Join(e.Root, "supervisor", "standalone")
	return execution.Profile{
		ExecutionID: id, ExecutionGeneration: 3, SandboxName: execution.OpenShellSandboxName(id),
		LifecycleFile: filepath.Join(root, id, "openshell.json"), GatewayName: "whirlwind-test-local",
		GatewayEndpoint: "http://127.0.0.1:8080", Workspace: "whirlwind-test", DeadlineAt: time.Now().Add(4 * time.Minute).UTC().Format(time.RFC3339Nano),
		CWD: cwd, Launcher: "/app/bridges/openshell/launch.mjs", SupervisorDir: root,
		ExecutionLabelValue: execution.OpenShellExecutionLabel(id, 3),
	}
}

func createStandaloneRecoverySandbox(t *testing.T, fixture *openshell.Fixture, profile execution.Profile) {
	t.Helper()
	args := []string{"sandbox", "create", "--name", profile.SandboxName, "--from", "fake-image", "--policy", "/tmp/policy", "--driver-config-json", "{}",
		"--label", "whirlwind.io/execution=" + profile.ExecutionLabelValue, "--detach", "--no-auto-providers", "--output", "json",
		"--gateway", profile.GatewayName, "--gateway-endpoint", profile.GatewayEndpoint, "--workspace", profile.Workspace,
		"--", "/bin/sh", "-c", "exec sleep infinity"}
	if _, err := fixture.RunCLI(t, args...); err != nil {
		t.Fatalf("create registered standalone runtime fixture: %v", err)
	}
}

func markStandaloneOwnerStale(t *testing.T, profile execution.Profile, pid int, started string) {
	t.Helper()
	path := execution.StandaloneRegistryPath(profile.SupervisorDir)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var registry map[string]any
	if err := json.Unmarshal(data, &registry); err != nil {
		t.Fatal(err)
	}
	executions, ok := registry["executions"].([]any)
	if !ok || len(executions) != 1 {
		t.Fatalf("expected one registry entry: %s", data)
	}
	entry, ok := executions[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected registry entry: %#v", executions[0])
	}
	entry["cwd"] = profile.CWD
	entry["deadline_at"] = profile.DeadlineAt
	entry["owner_pid"] = pid
	entry["owner_started"] = started
	data, err = json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeStandaloneLifecycle(t *testing.T, profile execution.Profile) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(profile.LifecycleFile), 0700); err != nil {
		t.Fatal(err)
	}
	identity := execution.SandboxLifecycle{
		ExecutionID: profile.ExecutionID, ExecutionGeneration: profile.ExecutionGeneration,
		SandboxName: profile.SandboxName, SandboxID: "test-sandbox-" + profile.SandboxName,
		GatewayName: profile.GatewayName, GatewayEndpoint: profile.GatewayEndpoint, Workspace: profile.Workspace,
		ExecutionLabelValue: profile.ExecutionLabelValue, DeadlineAt: profile.DeadlineAt,
		SandboxPhase: "Ready", SandboxHealth: "ready",
	}
	data, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile.LifecycleFile, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func stopEngineOnStartup(t *testing.T, engine *Engine) {
	t.Helper()
	settingsRecord, err := engine.Store.Get(context.Background(), "settings", "global")
	if err != nil {
		t.Fatal(err)
	}
	settings := engine.settings(context.Background())
	settings.Stopped = true
	if err := engine.Store.Put(context.Background(), "settings", "global", settings, settingsRecord.Version); err != nil {
		t.Fatal(err)
	}
	if !engine.settings(context.Background()).Stopped {
		t.Fatal("fixture must disable background reconciliation to isolate the Engine.Run startup hook")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		engine.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := execution.ReadStandaloneRuntimes(filepath.Join(engine.Root, "supervisor", "standalone"))
		if err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		if len(entries) == 0 {
			cancel()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	entries, err := execution.ReadStandaloneRuntimes(filepath.Join(engine.Root, "supervisor", "standalone"))
	t.Fatalf("Engine.Run did not reconcile stale standalone runtime at startup: entries=%+v err=%v", entries, err)
}
