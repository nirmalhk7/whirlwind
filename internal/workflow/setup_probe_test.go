package workflow

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
)

// This external peer rejects inference and verifies model then thinking selection.
func TestSetupProbePeer(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "setup-probe-peer" {
		return
	}
	modelSet := false
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var message provider.Message
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			os.Exit(2)
		}
		var params map[string]any
		_ = json.Unmarshal(message.Params, &params)
		result := setupPeerResult(message.Method, params, &modelSet)
		if json.NewEncoder(os.Stdout).Encode(map[string]any{"id": message.ID, "result": result}) != nil {
			os.Exit(5)
		}
	}
	os.Exit(0)
}

func probeOptions(selected, effort string) []any {
	return []any{
		map[string]any{"id": "model", "category": "model", "type": "select", "currentValue": selected, "options": []any{map[string]any{"value": "configured", "name": "Configured"}}},
		map[string]any{"id": "reasoning_effort", "category": "thought_level", "type": "select", "currentValue": effort, "options": []any{map[string]any{"value": "low", "name": "Low"}}},
	}
}

func seedSetupProbe(t *testing.T) (*Engine, model.Project) {
	return seedSetupProbeWithSource(t, nil)
}

func seedSetupProbeWithACP(t *testing.T) (*Engine, model.Project, *openshell.Fixture) {
	fixture := installSetupProbeOpenShell(t)
	e, p := seedSetupProbeWithSource(t, fixture)
	return e, p, fixture
}

func installSetupProbeOpenShell(t *testing.T) *openshell.Fixture {
	t.Helper()
	fixture := openshell.Install(t)
	fixture.EnableCommandExecution()
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "test-image")
	t.Setenv("WHIRLWIND_OPENSHELL_PROXY_TOKEN", strings.Repeat("a", 64))
	return fixture
}

func seedSetupProbeWithSource(t *testing.T, fixture *openshell.Fixture) (*Engine, model.Project) {
	t.Helper()
	e, task, cwd := testEngine(t)
	cwd, _ = filepath.EvalSymlinks(cwd)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	record, _ := e.Store.Get(ctx, "providers", "fake")
	source, _ := store.Decode[model.Provider](record)
	peerCommand := []string{exe, "-test.run=^TestSetupProbePeer$", "--", "setup-probe-peer"}
	if fixture == nil {
		// Retained for nonmigrated setup helper users; the exact-ACP cases below
		// install the controlled OpenShell runtime and use this command directly.
		source.Kind = "codex"
		source.Command = []string{"must-not-use-native-health"}
		source.ModelCommand = peerCommand
	} else {
		source.Kind = "acp"
		source.ACPProfile = ""
		source.Command = peerCommand
		source.ModelCommand = nil
	}
	if err = e.Store.Put(ctx, "providers", source.ID, source, record.Version); err != nil {
		t.Fatal(err)
	}
	run := agentRun{Engine: e, ctx: ctx, project: p, task: task, input: provider.Input{Policy: p.Policy, Role: "ideate", CWD: cwd, ReadOnly: true, Model: "configured", Thinking: &model.ThinkingChoice{ConfigID: "reasoning_effort", Value: "low"}}}
	if err = run.prepareExecutionProfile(source); err != nil {
		t.Fatal(err)
	}
	failure := &setupFailure{cause: &provider.StartupError{Cause: errors.New("exit status 127"), Diagnostic: "node unavailable"}, source: source, input: run.input}
	if err = e.saveSetupBlock(ctx, p, failure); err != nil {
		t.Fatal(err)
	}
	return e, p
}

func TestSetupProbeUsesExactACPAndRecovers(t *testing.T) {
	e, p, fixture := seedSetupProbeWithACP(t)
	blocked, recovered, err := e.checkSetupBlock(context.Background(), p)
	if err != nil || blocked || !recovered {
		r, _ := e.Store.Get(context.Background(), "setup_blocks", p.ID)
		t.Fatalf("exact ACP probe did not recover: %v %v %v %s", blocked, recovered, err, r.Data)
	}
	r, _ := e.Store.Get(context.Background(), "setup_blocks", p.ID)
	b, _ := store.Decode[setupBlock](r)
	if b.Active || b.CheckedAt == "" {
		t.Fatalf("recovery not persisted: %+v", b)
	}
	assertSetupProbeRuntimeClean(t, e, p, fixture)
}

func TestSetupRecoveryDoesNotRepeatCompletedSchedule(t *testing.T) {
	e, p := seedSetupProbe(t)
	ctx := context.Background()
	key := p.ID + ":2026-11-04"
	if err := e.Store.Put(ctx, "schedules", key, map[string]any{"project": p.ID, "stage": "complete", "day": setupRecoveryDay}, 0); err != nil {
		t.Fatal(err)
	}
	before, _ := e.Store.Get(ctx, "schedules", key)
	e.GitHub = disconnectedGitHub{}
	if err := e.runDailyProject(ctx, p, setupRecoveryDay); err != nil {
		t.Fatal(err)
	}
	after, _ := e.Store.Get(ctx, "schedules", key)
	if after.Version != before.Version {
		t.Fatalf("completed day reran after setup recovery: %s", after.Data)
	}
}

func TestChangedProviderProbesNewConfigurationImmediately(t *testing.T) {
	e, p, fixture := seedSetupProbeWithACP(t)
	ctx := context.Background()
	r, _ := e.Store.Get(ctx, "setup_blocks", p.ID)
	b, _ := store.Decode[setupBlock](r)
	b.NextCheckAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	if err := e.Store.Put(ctx, "setup_blocks", p.ID, b, r.Version); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Store.Get(ctx, "providers", b.Source.ID)
	source, _ := store.Decode[model.Provider](r)
	source.Command = append([]string{source.Command[0], "-test.count=1"}, source.Command[1:]...)
	if err := e.Store.Put(ctx, "providers", source.ID, source, r.Version); err != nil {
		t.Fatal(err)
	}
	blocked, recovered, err := e.checkSetupBlock(ctx, p)
	if err != nil || blocked || !recovered {
		t.Fatalf("changed configuration stayed blocked: %v %v %v", blocked, recovered, err)
	}
	assertSetupProbeRuntimeClean(t, e, p, fixture)
}

func assertSetupProbeRuntimeClean(t *testing.T, e *Engine, p model.Project, fixture *openshell.Fixture) {
	t.Helper()
	root := filepath.Join(e.Root, "supervisor", "standalone")
	entries, err := execution.ReadStandaloneRuntimes(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("completed setup probe left an unresolved runtime locator: %+v %v", entries, err)
	}
	operations := fixture.Calls(t)
	workdir := setupProbeWorkdir(t, operations)
	wantSegment := filepath.ToSlash(filepath.Join("ideation", safe(p.ID), "setup-probe"))
	if workdir == p.Path || !strings.Contains(filepath.ToSlash(workdir), "/"+wantSegment+"-") {
		t.Fatalf("setup probe did not use a project-owned disposable worktree: workdir=%q want_segment=%q project=%q", workdir, wantSegment, p.Path)
	}
	if _, err := os.Lstat(workdir); !os.IsNotExist(err) {
		t.Fatalf("setup probe worktree remains after verified cleanup: %s (%v)", workdir, err)
	}
	for _, operation := range []string{"create", "exec", "stop", "delete", "list"} {
		if !hasSetupProbeSandboxOperation(operations, operation) {
			t.Fatalf("setup probe did not complete OpenShell %s lifecycle: %v", operation, operations)
		}
	}
}

func setupProbeWorkdir(t *testing.T, calls [][]string) string {
	t.Helper()
	workdir := ""
	for _, call := range calls {
		if len(call) < 3 || call[0] != "sandbox" || call[1] != "exec" {
			continue
		}
		for i := 2; i+1 < len(call); i++ {
			if call[i] == "--workdir" {
				workdir = call[i+1]
				break
			}
		}
	}
	if workdir == "" {
		t.Fatal("setup probe OpenShell exec had no working directory")
	}
	return workdir
}

func hasSetupProbeSandboxOperation(calls [][]string, operation string) bool {
	for _, call := range calls {
		if len(call) >= 2 && call[0] == "sandbox" && call[1] == operation {
			return true
		}
	}
	return false
}

func TestSetupProbeLeaseAndCachePreventConcurrentLaunches(t *testing.T) {
	e, p := seedSetupProbe(t)
	ctx := context.Background()
	row, _ := e.Store.Get(ctx, "setup_blocks", p.ID)
	b, _ := store.Decode[setupBlock](row)
	lease, err := e.Store.Claim(ctx, "setup-probe:"+b.Fingerprint, "other", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	blocked, _, err := e.checkSetupBlock(ctx, p)
	if err != nil || !blocked {
		t.Fatalf("concurrent probe lease ignored: %v", err)
	}
	if err = e.Store.Release(ctx, "setup-probe:"+b.Fingerprint, "other", lease.Fence); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { _, _, _ = e.checkSetupBlock(ctx, p) })
	}
	wg.Wait()
	rows, err := e.Store.List(ctx, "setup_probes")
	if err != nil || len(rows) != 1 || rows[0].Version != 1 {
		t.Fatalf("probe launched more than once: %+v %v", rows, err)
	}
}

func TestSetupBlockStoppedAndCancelledRemainActive(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "stop"}[stop], func(t *testing.T) { assertSetupStopped(t, stop) })
	}
}

func assertSetupStopped(t *testing.T, stop bool) {
	t.Helper()
	e, p := seedSetupProbe(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if stop {
		r, _ := e.Store.Get(ctx, "settings", "global")
		settings := e.settings(ctx)
		settings.Stopped = true
		if err := e.Store.Put(ctx, "settings", "global", settings, r.Version); err != nil {
			t.Fatal(err)
		}
	} else {
		cancel()
	}
	if err := e.runDailyProject(ctx, p, setupRecoveryDay); err == nil {
		t.Fatal("cancelled/stopped scheduler proceeded")
	}
	r, _ := e.Store.Get(context.Background(), "setup_blocks", p.ID)
	if !strings.Contains(string(r.Data), `"active":true`) {
		t.Fatal("cancelled block cleared")
	}
	rows, _ := e.Store.List(context.Background(), "setup_probes")
	if len(rows) != 0 {
		t.Fatal("probe launched while stopped")
	}
}

func setupPeerResult(method string, params map[string]any, modelSet *bool) any {
	var result any
	switch method {
	case "initialize":
		result = map[string]any{"protocolVersion": 1}
	case "session/new":
		result = map[string]any{"sessionId": "probe", "configOptions": probeOptions("default", "high")}
	case "session/set_config_option":
		if params["configId"] == "model" && params["value"] == "configured" {
			*modelSet = true
			result = map[string]any{"configOptions": probeOptions("configured", "high")}
		} else if *modelSet && params["configId"] == "reasoning_effort" && params["value"] == "low" {
			result = map[string]any{"configOptions": probeOptions("configured", "low")}
		} else {
			os.Exit(3)
		}
	default:
		os.Exit(4)
	}
	return result
}
