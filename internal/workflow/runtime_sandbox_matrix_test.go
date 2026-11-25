package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
)

const runtimeMatrixUnhealthyModel = "unhealthy-nono-runtime"

type runtimeMatrixUnhealthyAdapter struct {
	observations []runtimeMatrixObservation
	profiles     []execution.Profile
	failures     int
	fallbacks    int
}

func (a *runtimeMatrixUnhealthyAdapter) Run(ctx context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
	observation := runtimeMatrixObservation{role: input.Role, model: input.Model, thinking: input.Thinking}
	if input.Execution != nil {
		observation.profile = input.Execution.Digest()
	}
	a.observations = append(a.observations, observation)
	if input.Model != runtimeMatrixUnhealthyModel {
		if input.SessionID != "" {
			return provider.Output{}, fmt.Errorf("session crossed model fallback: %q", input.SessionID)
		}
		a.fallbacks++
		if input.Execution != nil && execution.IsNonoLauncher(input.Execution.Launcher) {
			if err := initializeRuntimeMatrixFallback(ctx, *input.Execution); err != nil {
				return provider.Output{}, err
			}
		}
		return provider.Output{Text: runtimeMatrixSuccessfulOutput(input.Role), SessionID: "fallback-session"}, nil
	}
	if input.Execution == nil || !execution.IsManagedLauncher(input.Execution.Launcher) {
		return provider.Output{}, errors.New("runtime failure matrix did not receive the pinned managed profile")
	}
	a.failures++
	return a.runWithUnhealthySandbox(ctx, input)
}

func initializeRuntimeMatrixFallback(ctx context.Context, profile execution.Profile) error {
	command := []string{"/bin/sh", "-c", `IFS= read -r request; id=$(printf '%s' "$request" | sed -E 's/.*"id":([^,}]+).*/\1/'); printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"`}
	rpc, err := provider.StartWithProfile(ctx, command, profile, nil)
	if err != nil {
		return err
	}
	if err := rpc.Call(ctx, "initialize", map[string]any{"protocolVersion": 1}, nil); err != nil {
		return errors.Join(err, rpc.Close())
	}
	if err := rpc.Close(); err != nil {
		return err
	}
	return nil
}

func (a *runtimeMatrixUnhealthyAdapter) runWithUnhealthySandbox(ctx context.Context, input provider.Input) (provider.Output, error) {
	profile := *input.Execution
	a.profiles = append(a.profiles, profile)
	command := []string{"/bin/sh", "-c", `IFS= read -r request; id=$(printf '%s' "$request" | sed -E 's/.*"id":([^,}]+).*/\1/'); printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"; IFS= read -r hold; sleep 60`}
	exitEvents := make(chan map[string]any, 1)
	rpc, err := provider.StartWithProfile(ctx, command, profile, nil, func(kind string, value any) {
		if kind == "whirlwind/process_exit" {
			if event, ok := value.(map[string]any); ok {
				exitEvents <- event
			}
		}
	})
	if err != nil {
		return provider.Output{}, err
	}
	if err := rpc.Call(ctx, "initialize", map[string]any{"protocolVersion": 1}, nil); err != nil {
		return provider.Output{}, errors.Join(fmt.Errorf("nono child did not answer initialize: %w", err), rpc.Close())
	}
	holdResult := make(chan error, 1)
	go func() { holdResult <- rpc.Call(ctx, "hold", map[string]any{"after": "initialize"}, nil) }()
	proof, exists, err := execution.ReadRuntimeLifecycle(profile)
	if err != nil || !exists || proof.SandboxID == "" || proof.SandboxPhase != "Ready" || proof.SandboxHealth != "healthy" {
		return provider.Output{}, errors.Join(fmt.Errorf("nono did not persist a healthy resource identity: exists=%t proof=%+v err=%v", exists, proof, err), rpc.Close())
	}
	proof.SandboxPhase = "Error"
	proof.SandboxHealth = "unavailable"
	proof.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := writeRuntimeProofForMatrix(profile.LifecycleFile, proof); err != nil {
		return provider.Output{}, errors.Join(fmt.Errorf("mark controlled sandbox health loss: %w", err), rpc.Close())
	}
	select {
	case <-ctx.Done():
	case <-time.After(8 * time.Second):
		return provider.Output{}, errors.Join(errors.New("worker heartbeat did not stop the adapter after nono health loss"), rpc.Close())
	}
	select {
	case holdErr := <-holdResult:
		if holdErr == nil {
			return provider.Output{}, errors.Join(errors.New("health loss did not interrupt the active ACP request"), rpc.Close())
		}
	case <-time.After(2 * time.Second):
		return provider.Output{}, errors.Join(errors.New("health loss did not close the in-flight ACP transport"), rpc.Close())
	}
	closeErr := rpc.Close()
	if closeErr != nil {
		return provider.Output{}, closeErr
	}
	select {
	case exit := <-exitEvents:
		if exit["cleanup_verified"] != true {
			return provider.Output{}, fmt.Errorf("nono cleanup was not verified before retry: %v", exit)
		}
	default:
		return provider.Output{}, errors.New("provider process exited without a cleanup observation")
	}
	return provider.Output{}, ctx.Err()
}

func writeRuntimeProofForMatrix(path string, proof execution.SandboxLifecycle) error {
	data, err := json.Marshal(proof)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func TestNonoHealthRecoveryMatrixAcrossCanonicalRoles(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native macOS execution acceptance")
	}
	fixture := openshell.Install(t)
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "test-image")
	t.Setenv("WHIRLWIND_OPENSHELL_PROXY_TOKEN", strings.Repeat("a", 64))
	_, source, _, _ := runtime.Caller(0)
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", filepath.Join(filepath.Dir(source), "..", "..", "bridges", "nono", "launch.mjs"))
	for _, role := range model.AgentRoles() {
		t.Run(role, func(t *testing.T) {
			assertNonoHealthRecoveryRole(t, role)
		})
	}
}

func assertNativeSandboxMatrixPersistence(t *testing.T, database *store.Store, role string, profiles []execution.Profile) {
	t.Helper()
	records, err := database.List(context.Background(), "sessions")
	requireWorkflowSuccess(t, err)
	failed := 0
	for _, record := range records {
		attempt, err := store.Decode[model.Attempt](record)
		requireWorkflowSuccess(t, err)
		if attempt.Role != role || attempt.Model != runtimeMatrixUnhealthyModel {
			continue
		}
		failed++
		// Native cleanup records Stopped after the health loss; preserve the
		// observed failure in Error while proving the terminal process is gone.
		if attempt.RuntimeKind != "nono" || !strings.Contains(attempt.Error, ErrRuntimeHealthLost.Error()+": phase=Error health=unavailable") || attempt.Status != "failed" || !attempt.Finished || attempt.SandboxPhase != "Stopped" || attempt.SandboxHealth != "stopped" {
			t.Fatalf("native health failure lacks persisted evidence: %+v", attempt)
		}
		assertStoppedNativeProfile(t, attempt, profiles)
	}
	if failed != 4 || len(profiles) != 4 {
		t.Fatalf("native recovery lost attempts: failed=%d profiles=%d", failed, len(profiles))
	}
}

func assertNonoHealthRecoveryRole(t *testing.T, role string) {
	t.Helper()
	t.Parallel()
	engine, task, cwd := testEngine(t)
	cwd, err := filepath.EvalSymlinks(cwd)
	requireWorkflowSuccess(t, err)
	project, err := engine.Project(context.Background(), task.ProjectID)
	requireWorkflowSuccess(t, err)
	firstChoice := model.AgentChoice{Provider: "fake", Model: runtimeMatrixUnhealthyModel, Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}}
	fallbackChoice := model.AgentChoice{Provider: "fake", Model: "fallback-model", Thinking: &model.ThinkingChoice{ConfigID: "reasoning", Value: "low"}}
	putFixtureAgent(t, engine.Store, role, model.AgentConfig{Role: role, Chain: []model.AgentChoice{firstChoice, fallbackChoice}})
	adapter := &runtimeMatrixUnhealthyAdapter{}
	engine.Adapter = func(model.Provider) provider.Adapter { return adapter }
	ctx, cancel := context.WithCancel(context.Background())
	var firstBackoff []time.Duration
	engine.retryWait = func(waitCtx context.Context, delay time.Duration) error {
		firstBackoff = append(firstBackoff, delay)
		cancel()
		return waitCtx.Err()
	}
	_, firstErr := engine.agent(ctx, project, task, provider.Input{Role: role, CWD: cwd, Prompt: "recover lost nono health"})
	cancel()
	if !errors.Is(firstErr, context.Canceled) || !reflect.DeepEqual(firstBackoff, []time.Duration{time.Second}) || adapter.failures != 1 {
		t.Fatalf("first health failure was not persisted before supervisor interruption: failures=%d backoff=%v state=%s err=%v", adapter.failures, firstBackoff, runtimeMatrixPersistedState(t, engine, project, task, role, firstChoice), firstErr)
	}
	priorOwner := engine.Owner()
	storePath := filepath.Join(engine.Root, "state.db")
	requireWorkflowSuccess(t, engine.Store.Close())
	reopened, err := store.Open(storePath)
	requireWorkflowSuccess(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := New(reopened, engine.GitHub, engine.Root)
	if restarted.Owner() == priorOwner {
		t.Fatal("recreated supervisor reused the prior runtime owner")
	}
	restarted.Adapter = func(model.Provider) provider.Adapter { return adapter }
	var resumedBackoff []time.Duration
	restarted.retryWait = func(_ context.Context, delay time.Duration) error {
		resumedBackoff = append(resumedBackoff, delay)
		if len(resumedBackoff) == 1 {
			time.Sleep(delay)
		}
		return nil
	}
	taskRecord, err := reopened.Get(context.Background(), "execution_plans", task.ID)
	requireWorkflowSuccess(t, err)
	currentTask, err := store.Decode[model.ExecutionPlan](taskRecord)
	requireWorkflowSuccess(t, err)
	output, err := restarted.agent(context.Background(), project, currentTask, provider.Input{Role: role, CWD: cwd, Prompt: "recover lost nono health"})
	if err != nil || output.SessionID != "fallback-session" || adapter.failures != 4 || adapter.fallbacks != 1 {
		t.Fatalf("health loss did not exhaust three same-choice restarts then fallback: failures=%d fallback=%d output=%+v err=%v state=%s", adapter.failures, adapter.fallbacks, output, err, runtimeMatrixPersistedState(t, restarted, project, currentTask, role, firstChoice))
	}
	if len(resumedBackoff) != 3 || resumedBackoff[0] <= 0 || resumedBackoff[0] > time.Second || resumedBackoff[1] != 2*time.Second || resumedBackoff[2] != 4*time.Second {
		t.Fatalf("health recovery backoff=%v, want persisted 1s remainder then 2s and 4s", resumedBackoff)
	}
	assertRuntimeMatrixAttempts(t, reopened, role, runtimeAttemptExpectations{
		first: firstChoice, fallback: fallbackChoice, priorOwner: priorOwner, currentOwner: restarted.Owner(),
	})
	assertNativeSandboxMatrixPersistence(t, reopened, role, adapter.profiles)
}

func assertStoppedNativeProfile(t *testing.T, attempt model.Attempt, profiles []execution.Profile) {
	t.Helper()
	matched := false
	for _, profile := range profiles {
		if profile.ExecutionID != attempt.ExecutionID {
			continue
		}
		matched = true
		proof, exists, err := execution.ReadRuntimeLifecycle(profile)
		if err != nil || !exists || proof.ExecutionGeneration != attempt.ExecutionGeneration || proof.SandboxPhase != "Stopped" || proof.SandboxHealth != "stopped" || proof.ActiveProcesses != 0 {
			t.Fatalf("native failed process not fenced and cleaned: %+v %v", proof, err)
		}
	}
	if !matched {
		t.Fatal("native attempt lost pinned runtime identity")
	}
}
