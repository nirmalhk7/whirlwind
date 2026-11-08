package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
)

const runtimeMatrixUnhealthyModel = "unhealthy-openshell-runtime"

type runtimeMatrixUnhealthyAdapter struct {
	fixture      *openshell.Fixture
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
		return provider.Output{Text: runtimeMatrixSuccessfulOutput(input.Role), SessionID: "fallback-session"}, nil
	}
	if input.Execution == nil || !execution.IsOpenShellLauncher(input.Execution.Launcher) {
		return provider.Output{}, errors.New("runtime failure matrix did not receive the pinned OpenShell profile")
	}
	a.failures++
	return a.runWithUnhealthySandbox(ctx, input)
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
		return provider.Output{}, errors.Join(fmt.Errorf("OpenShell child did not answer initialize: %w", err), rpc.Close())
	}
	holdResult := make(chan error, 1)
	go func() { holdResult <- rpc.Call(ctx, "hold", map[string]any{"after": "initialize"}, nil) }()
	proof, exists, err := execution.ReadOpenShellLifecycle(profile)
	if err != nil || !exists || proof.SandboxID == "" || proof.SandboxPhase != "Ready" || proof.SandboxHealth != "healthy" {
		return provider.Output{}, errors.Join(fmt.Errorf("OpenShell did not persist a healthy resource identity: exists=%t proof=%+v err=%v", exists, proof, err), rpc.Close())
	}
	proof.SandboxPhase = "Error"
	proof.SandboxHealth = "unavailable"
	proof.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := writeOpenShellProofForMatrix(profile.LifecycleFile, proof); err != nil {
		return provider.Output{}, errors.Join(fmt.Errorf("mark controlled sandbox health loss: %w", err), rpc.Close())
	}
	select {
	case <-ctx.Done():
	case <-time.After(8 * time.Second):
		return provider.Output{}, errors.Join(errors.New("worker heartbeat did not stop the adapter after OpenShell health loss"), rpc.Close())
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
			return provider.Output{}, fmt.Errorf("OpenShell cleanup was not verified before retry: %v", exit)
		}
	default:
		return provider.Output{}, errors.New("provider process exited without a cleanup observation")
	}
	return provider.Output{}, ctx.Err()
}

func writeOpenShellProofForMatrix(path string, proof execution.SandboxLifecycle) error {
	data, err := json.Marshal(proof)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func TestSandboxHealthRecoveryMatrixAcrossCanonicalRoles(t *testing.T) {
	fixture := openshell.Install(t)
	fixture.EnableCommandExecution()
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "test-image")
	t.Setenv("WHIRLWIND_OPENSHELL_PROXY_TOKEN", strings.Repeat("a", 64))
	for _, role := range model.AgentRoles() {
		t.Run(role, func(t *testing.T) {
			fixture.ResetCalls(t)
			engine, task, cwd := testEngine(t)
			cwd, err := filepath.EvalSymlinks(cwd)
			if err != nil {
				t.Fatal(err)
			}
			project, err := engine.Project(context.Background(), task.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			firstChoice := model.AgentChoice{Provider: "fake", Model: runtimeMatrixUnhealthyModel, Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}}
			fallbackChoice := model.AgentChoice{Provider: "fake", Model: "fallback-model", Thinking: &model.ThinkingChoice{ConfigID: "reasoning", Value: "low"}}
			putFixtureAgent(t, engine.Store, role, model.AgentConfig{Role: role, Chain: []model.AgentChoice{firstChoice, fallbackChoice}})
			adapter := &runtimeMatrixUnhealthyAdapter{fixture: fixture}
			engine.Adapter = func(model.Provider) provider.Adapter { return adapter }
			ctx, cancel := context.WithCancel(context.Background())
			var firstBackoff []time.Duration
			engine.retryWait = func(waitCtx context.Context, delay time.Duration) error {
				firstBackoff = append(firstBackoff, delay)
				cancel()
				return waitCtx.Err()
			}
			_, firstErr := engine.agent(ctx, project, task, provider.Input{Role: role, CWD: cwd, Prompt: "recover lost OpenShell health"})
			cancel()
			if !errors.Is(firstErr, context.Canceled) || !reflect.DeepEqual(firstBackoff, []time.Duration{time.Second}) || adapter.failures != 1 {
				t.Fatalf("first health failure was not persisted before supervisor interruption: failures=%d backoff=%v state=%s err=%v", adapter.failures, firstBackoff, runtimeMatrixPersistedState(t, engine, project, task, role, firstChoice), firstErr)
			}
			priorOwner := engine.Owner()
			storePath := filepath.Join(engine.Root, "state.db")
			if err := engine.Store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(storePath)
			if err != nil {
				t.Fatal(err)
			}
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
			taskRecord, err := reopened.Get(context.Background(), "tasks", task.ID)
			if err != nil {
				t.Fatal(err)
			}
			currentTask, err := store.Decode[model.Task](taskRecord)
			if err != nil {
				t.Fatal(err)
			}
			output, err := restarted.agent(context.Background(), project, currentTask, provider.Input{Role: role, CWD: cwd, Prompt: "recover lost OpenShell health"})
			if err != nil || output.SessionID != "fallback-session" || adapter.failures != 4 || adapter.fallbacks != 1 {
				t.Fatalf("health loss did not exhaust three same-choice restarts then fallback: failures=%d fallback=%d output=%+v err=%v state=%s", adapter.failures, adapter.fallbacks, output, err, runtimeMatrixPersistedState(t, restarted, project, currentTask, role, firstChoice))
			}
			if len(resumedBackoff) != 3 || resumedBackoff[0] <= 0 || resumedBackoff[0] > time.Second || resumedBackoff[1] != 2*time.Second || resumedBackoff[2] != 4*time.Second {
				t.Fatalf("health recovery backoff=%v, want persisted 1s remainder then 2s and 4s", resumedBackoff)
			}
			assertRuntimeMatrixAttempts(t, reopened, role, firstChoice, fallbackChoice, priorOwner, restarted.Owner())
			assertSandboxMatrixPersistence(t, reopened, role, adapter.profiles)
			assertSandboxMatrixResourcesRemoved(t, fixture, adapter.profiles)
		})
	}
}

func assertSandboxMatrixResourcesRemoved(t *testing.T, fixture *openshell.Fixture, profiles []execution.Profile) {
	t.Helper()
	if len(profiles) != 4 {
		t.Fatalf("pinned OpenShell profiles=%d, want four failed runtime attempts", len(profiles))
	}
	for _, profile := range profiles {
		output, err := fixture.RunCLI(t, "sandbox", "list", "--output", "json", "--selector", "whirlwind.io/execution="+profile.ExecutionLabelValue, "--gateway", profile.GatewayName, "--gateway-endpoint", profile.GatewayEndpoint, "--workspace", profile.Workspace)
		if err != nil {
			t.Fatalf("query persisted sandbox identity after cleanup: %v", err)
		}
		var page struct {
			Sandboxes []struct {
				Name string `json:"name"`
			} `json:"sandboxes"`
		}
		if err := json.Unmarshal(output, &page); err != nil {
			t.Fatal(err)
		}
		for _, operation := range []string{"stop", "delete"} {
			if !sandboxMatrixRecordedOperation(fixture.Calls(t), operation, profile.SandboxName) {
				t.Fatalf("cleanup omitted %s for pinned sandbox %s", operation, profile.SandboxName)
			}
		}
		for _, sandbox := range page.Sandboxes {
			if sandbox.Name == profile.SandboxName {
				t.Fatalf("failed runtime %s still owns sandbox %s after replacement", profile.ExecutionID, sandbox.Name)
			}
		}
	}
}

func assertSandboxMatrixPersistence(t *testing.T, database *store.Store, role string, profiles []execution.Profile) {
	t.Helper()
	rows, err := database.List(context.Background(), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	var attempts []model.Attempt
	for _, row := range rows {
		attempt, err := store.Decode[model.Attempt](row)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.Role == role && attempt.Model == runtimeMatrixUnhealthyModel {
			attempts = append(attempts, attempt)
		}
	}
	sort.Slice(attempts, func(i, j int) bool { return attempts[i].StartedAt < attempts[j].StartedAt })
	if len(attempts) != 4 || len(profiles) != 4 {
		t.Fatalf("persisted failed runtime attempts=%d, profiles=%d, want four", len(attempts), len(profiles))
	}
	profileByLifecycle := make(map[string]execution.Profile, len(profiles))
	for _, profile := range profiles {
		profileByLifecycle[profile.LifecycleFile] = profile
	}
	for index, attempt := range attempts {
		if attempt.RuntimeKind != "openshell" || attempt.SandboxID == "" || attempt.SandboxName == "" || attempt.LifecycleFile == "" || attempt.SandboxPhase != "Error" || attempt.SandboxHealth != "unavailable" || !strings.Contains(attempt.Error, ErrRuntimeHealthLost.Error()) {
			t.Fatalf("health failure did not preserve exact OpenShell identity and typed outcome at attempt %d: %+v", index, attempt)
		}
		profile, ok := profileByLifecycle[attempt.LifecycleFile]
		if !ok || profile.ExecutionID != attempt.ExecutionID || profile.ExecutionGeneration != attempt.ExecutionGeneration || profile.SandboxName != attempt.SandboxName {
			t.Fatalf("attempt %d did not bind persisted ownership to its exact pinned profile: %+v profile=%+v", index, attempt, profile)
		}
		proof, exists, err := execution.ReadOpenShellLifecycle(profile)
		if err != nil || !exists || proof.SandboxID != attempt.SandboxID || proof.SandboxPhase != attempt.SandboxPhase || proof.SandboxHealth != attempt.SandboxHealth {
			t.Fatalf("attempt %d lifecycle proof does not match persisted failed runtime: exists=%t proof=%+v err=%v", index, exists, proof, err)
		}
	}
}

func sandboxMatrixRecordedOperation(calls [][]string, operation, name string) bool {
	for _, call := range calls {
		if len(call) >= 3 && call[0] == "sandbox" && call[1] == operation && call[2] == name {
			return true
		}
	}
	return false
}
