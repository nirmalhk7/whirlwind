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
	startedAt    []time.Time
	disconnects  int
	fallbacks    int
}

func (a *runtimeMatrixDisconnectAdapter) Run(ctx context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
	a.startedAt = append(a.startedAt, time.Now())
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
		t.Run(role, func(t *testing.T) { runTransportDisconnectRecovery(t, role, false) })
	}
}

func TestTransportDisconnectRecoveryAfterPersistedBackoffElapsed(t *testing.T) {
	runTransportDisconnectRecovery(t, "ideate", true)
}

func runTransportDisconnectRecovery(t *testing.T, role string, expireBackoff bool) {
	t.Helper()
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
	retryValue := transportMatrixRetryState(t, firstRun, firstChoice, 1)
	retryAt, err := time.Parse(time.RFC3339Nano, retryValue)
	if err != nil {
		t.Fatal(err)
	}
	priorOwner := engine.Owner()
	restarted := reopenTransportMatrixEngine(t, engine)
	reopened := restarted.Store
	restarted.Adapter = func(model.Provider) provider.Adapter { return adapter }
	var resumedBackoff []time.Duration
	restarted.retryWait = func(_ context.Context, delay time.Duration) error {
		resumedBackoff = append(resumedBackoff, delay)
		if delay <= time.Second {
			time.Sleep(delay)
		}
		return nil
	}
	taskRecord, err := reopened.Get(context.Background(), "execution_plans", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentTask, err := store.Decode[model.ExecutionPlan](taskRecord)
	if err != nil {
		t.Fatal(err)
	}
	restoredRun := &agentRun{Engine: restarted, ctx: context.Background(), project: project, task: currentTask, input: provider.Input{Role: role}}
	assertRestoredTransportRetry(t, restoredRun, firstChoice, retryValue)
	if expireBackoff {
		waitForTransportDeadline(retryAt)
	}
	output, err := restarted.agent(context.Background(), project, currentTask, provider.Input{Role: role, CWD: cwd, Prompt: "recover transport disconnect"})
	if err != nil || output.SessionID != "fallback-session" || adapter.disconnects != 4 || adapter.fallbacks != 1 {
		state := runtimeMatrixPersistedState(t, restarted, project, currentTask, role, firstChoice)
		t.Fatalf("recreated supervisor did not exhaust initial plus three transport restarts before fallback: disconnects=%d fallbacks=%d output=%+v err=%v persisted=%s", adapter.disconnects, adapter.fallbacks, output, err, state)
	}
	assertTransportMatrixBackoff(t, resumedBackoff, retryAt, adapter.startedAt[1], expireBackoff)
	assertTransportMatrixChoices(t, adapter, role, firstChoice, fallbackChoice)
	assertRuntimeMatrixAttempts(t, reopened, role, firstChoice, fallbackChoice, priorOwner, restarted.Owner())
	_ = transportMatrixRetryState(t, &agentRun{Engine: restarted, ctx: context.Background(), project: project, task: currentTask, input: provider.Input{Role: role}}, firstChoice, 4)
}

func reopenTransportMatrixEngine(t *testing.T, engine *Engine) *Engine {
	t.Helper()
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
	if restarted.Owner() == engine.Owner() {
		t.Fatal("recreated supervisor reused the prior runtime owner identity")
	}
	return restarted
}

func assertRestoredTransportRetry(t *testing.T, run *agentRun, choice model.AgentChoice, wantRetry string) {
	t.Helper()
	retry := transportMatrixRetryState(t, run, choice, 1)
	if retry != wantRetry {
		t.Fatalf("reopen changed persisted retry deadline: got=%q want=%q", retry, wantRetry)
	}
}

func transportMatrixRetryState(t *testing.T, run *agentRun, choice model.AgentChoice, want int) string {
	t.Helper()
	count, retry, err := run.persistedRestartState(choice)
	if err != nil || count != want {
		t.Fatalf("persisted restart count=%d, want %d: %v", count, want, err)
	}
	return retry
}

func waitForTransportDeadline(deadline time.Time) {
	if delay := time.Until(deadline); delay > 0 {
		time.Sleep(delay)
	}
}

func assertTransportMatrixBackoff(t *testing.T, waits []time.Duration, retryAt, firstRestart time.Time, expired bool) {
	t.Helper()
	if firstRestart.Before(retryAt) {
		t.Fatalf("restart launched at %s before persisted deadline %s", firstRestart, retryAt)
	}
	if expired && len(waits) != 2 {
		t.Fatalf("elapsed backoff was repeated: %v", waits)
	}
	remaining := waits
	if len(waits) == 3 {
		if waits[0] <= 0 || waits[0] > time.Second {
			t.Fatalf("invalid persisted backoff remainder: %v", waits)
		}
		remaining = waits[1:]
	}
	if !reflect.DeepEqual(remaining, []time.Duration{2 * time.Second, 4 * time.Second}) {
		t.Fatalf("resumed backoff=%v, want any unelapsed first remainder followed by 2s and 4s", waits)
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
