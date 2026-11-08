package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const runtimeMatrixDisconnectModel = "disconnecting-acp"

type runtimeMatrixDisconnectAdapter struct {
	observations []runtimeMatrixObservation
	disconnects  int
	fallbacks    int
}

func (a *runtimeMatrixDisconnectAdapter) Run(ctx context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
	observation := runtimeMatrixObservation{role: input.Role, model: input.Model, thinking: input.Thinking}
	if input.Execution != nil {
		observation.profile = input.Execution.Digest()
	}
	a.observations = append(a.observations, observation)
	if input.Model == runtimeMatrixDisconnectModel {
		a.disconnects++
		return provider.Output{}, disconnectACPTransport(ctx, input.CWD)
	}
	if input.SessionID != "" {
		return provider.Output{}, fmt.Errorf("session crossed model fallback: %q", input.SessionID)
	}
	a.fallbacks++
	return provider.Output{Text: runtimeMatrixSuccessfulOutput(input.Role), SessionID: "fallback-session"}, nil
}

func disconnectACPTransport(ctx context.Context, cwd string) error {
	pidFile := filepath.Join(cwd, ".whirlwind-disconnect-child.pid")
	command := []string{"/bin/sh", "-c", `printf '%s\n' "$$" > "$1"; IFS= read -r init; id=$(printf '%s' "$init" | sed -E 's/.*"id":([^,}]+).*/\1/'); printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"; IFS= read -r disconnect; kill -0 "$$" || exit 91; exec 1>&-; sleep 60`, "disconnect-child", pidFile}
	rpc, err := provider.Start(ctx, command, cwd, nil)
	if err != nil {
		return err
	}
	initializeErr := rpc.Call(ctx, "initialize", map[string]any{"protocolVersion": 1}, nil)
	if initializeErr != nil {
		return errors.Join(fmt.Errorf("ACP child did not complete its initial RPC response: %w", initializeErr), rpc.Close())
	}
	pid, err := readChildPID(pidFile)
	if err != nil {
		return errors.Join(err, rpc.Close())
	}
	if err := signalChild(pid); err != nil {
		return errors.Join(fmt.Errorf("ACP child was not alive after its successful initialize response: %w", err), rpc.Close())
	}
	disconnectErr := rpc.Call(ctx, "disconnect", map[string]any{"after": "initialize"}, nil)
	closeErr := rpc.Close()
	if disconnectErr == nil || !strings.Contains(strings.ToLower(disconnectErr.Error()), "provider exited before response") {
		return errors.Join(fmt.Errorf("ACP child did not disconnect after its successful initialize response: %v", disconnectErr), closeErr)
	}
	if closeErr != nil {
		return errors.Join(fmt.Errorf("ACP transport disconnect cleanup failed: %w", closeErr), disconnectErr)
	}
	if err := signalChild(pid); err == nil {
		return fmt.Errorf("ACP child process %d remained alive after transport cleanup", pid)
	}
	return disconnectErr
}

func readChildPID(path string) (int, error) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			var pid int
			if _, scanErr := fmt.Sscan(string(data), &pid); scanErr != nil || pid <= 0 {
				return 0, fmt.Errorf("invalid ACP child process id %q", data)
			}
			return pid, nil
		}
		if !os.IsNotExist(err) {
			return 0, err
		}
		time.Sleep(time.Millisecond)
	}
	return 0, errors.New("ACP child did not publish its process identity")
}

func signalChild(pid int) error {
	return syscall.Kill(pid, 0)
}

func TestProcessTransportDisconnectRecoveryMatrixAcrossCanonicalRoles(t *testing.T) {
	for _, role := range model.AgentRoles() {
		t.Run(role, func(t *testing.T) {
			engine, task, cwd := testEngine(t)
			var err error
			cwd, err = filepath.EvalSymlinks(cwd)
			if err != nil {
				t.Fatal(err)
			}
			project, err := engine.Project(context.Background(), task.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			firstChoice := model.AgentChoice{Provider: "fake", Model: runtimeMatrixDisconnectModel, Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}}
			fallbackChoice := model.AgentChoice{Provider: "fake", Model: "fallback-model", Thinking: &model.ThinkingChoice{ConfigID: "reasoning", Value: "low"}}
			putFixtureAgent(t, engine.Store, role, model.AgentConfig{Role: role, Chain: []model.AgentChoice{firstChoice, fallbackChoice}})
			adapter := &runtimeMatrixDisconnectAdapter{}
			engine.Adapter = func(model.Provider) provider.Adapter { return adapter }

			ctx, cancel := context.WithCancel(context.Background())
			var firstBackoff []time.Duration
			engine.retryWait = func(waitCtx context.Context, delay time.Duration) error {
				firstBackoff = append(firstBackoff, delay)
				cancel()
				return waitCtx.Err()
			}
			_, firstErr := engine.agent(ctx, project, task, provider.Input{Role: role, CWD: cwd, Prompt: "recover transport disconnect"})
			cancel()
			if !errors.Is(firstErr, context.Canceled) || adapter.disconnects != 1 || !reflect.DeepEqual(firstBackoff, []time.Duration{time.Second}) {
				t.Fatalf("first transport disconnect was not persisted before supervisor interruption: disconnects=%d backoff=%v persisted=%s err=%v", adapter.disconnects, firstBackoff, runtimeMatrixPersistedState(t, engine, project, task, role, firstChoice), firstErr)
			}

			firstRun := &agentRun{Engine: engine, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: role}}
			if restarts, _, err := firstRun.persistedRestartState(firstChoice); err != nil || restarts != 1 {
				t.Fatalf("persisted restart count after transport disconnect=%d, %v; want 1", restarts, err)
			}
			storePath := filepath.Join(engine.Root, "state.db")
			priorOwner := engine.Owner()
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
				t.Fatal("recreated supervisor reused the prior runtime owner identity")
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
			output, err := restarted.agent(context.Background(), project, currentTask, provider.Input{Role: role, CWD: cwd, Prompt: "recover transport disconnect"})
			if err != nil || output.SessionID != "fallback-session" || adapter.disconnects != 4 || adapter.fallbacks != 1 {
				state := runtimeMatrixPersistedState(t, restarted, project, currentTask, role, firstChoice)
				t.Fatalf("recreated supervisor did not exhaust initial plus three transport restarts before fallback: disconnects=%d fallbacks=%d output=%+v err=%v persisted=%s", adapter.disconnects, adapter.fallbacks, output, err, state)
			}
			if len(resumedBackoff) != 3 || resumedBackoff[0] <= 0 || resumedBackoff[0] > time.Second || resumedBackoff[1] != 2*time.Second || resumedBackoff[2] != 4*time.Second {
				t.Fatalf("resumed backoff=%v, want persisted 1s remainder then 2s and 4s", resumedBackoff)
			}
			assertTransportMatrixChoices(t, adapter, role, firstChoice, fallbackChoice)
			assertRuntimeMatrixAttempts(t, reopened, role, firstChoice, fallbackChoice, priorOwner, restarted.Owner())
			if restarts, _, err := (&agentRun{Engine: restarted, ctx: context.Background(), project: project, task: currentTask, input: provider.Input{Role: role}}).persistedRestartState(firstChoice); err != nil || restarts != 4 {
				t.Fatalf("restart budget after four transport disconnects=%d, %v; want persisted exhausted count 4", restarts, err)
			}
		})
	}
}

func assertTransportMatrixChoices(t *testing.T, adapter *runtimeMatrixDisconnectAdapter, role string, first, fallback model.AgentChoice) {
	t.Helper()
	if len(adapter.observations) != 5 {
		t.Fatalf("adapter executions=%d, want four disconnects and one fallback", len(adapter.observations))
	}
	for index, observation := range adapter.observations {
		if observation.role != role || observation.profile == "" || observation.profile != adapter.observations[0].profile {
			t.Fatalf("role or runtime profile changed across transport recovery: %+v", adapter.observations)
		}
		choice := first
		if index == 4 {
			choice = fallback
		}
		if observation.model != choice.Model || observation.thinking == nil || *observation.thinking != *choice.Thinking {
			t.Fatalf("configured role queue choice changed at attempt %d: %+v", index, observation)
		}
	}
}
