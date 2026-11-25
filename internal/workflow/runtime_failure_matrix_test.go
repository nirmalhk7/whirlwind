package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const (
	runtimeMatrixCrashModel      = "crashing-acp"
	runtimeMatrixFallbackSession = "fallback-session"
)

type runtimeMatrixObservation struct {
	role, model, profile string
	thinking             *model.ThinkingChoice
}

type runtimeMatrixAdapter struct {
	crashes        int
	lastCrashError string
	observations   []runtimeMatrixObservation
}

func (a *runtimeMatrixAdapter) Run(ctx context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
	observation := runtimeMatrixObservation{role: input.Role, model: input.Model, thinking: input.Thinking}
	if input.Execution != nil {
		observation.profile = input.Execution.Digest()
	}
	a.observations = append(a.observations, observation)
	if input.Model == runtimeMatrixCrashModel {
		a.crashes++
		err := crashACPChild(ctx, input.CWD)
		if err != nil {
			a.lastCrashError = err.Error()
		}
		return provider.Output{}, err
	}
	if input.SessionID != "" {
		return provider.Output{}, fmt.Errorf("session crossed model fallback: %q", input.SessionID)
	}
	return provider.Output{Text: runtimeMatrixSuccessfulOutput(input.Role), SessionID: runtimeMatrixFallbackSession}, nil
}

func crashACPChild(ctx context.Context, cwd string) error {
	rpc, err := provider.Start(ctx, []string{"/bin/sh", "-c", `IFS= read -r request; printf '%s\n' 'synthetic ACP child exited before response' >&2; exit 23`}, cwd, nil)
	if err != nil {
		return err
	}
	callErr := rpc.Call(ctx, "initialize", map[string]any{"protocolVersion": 1}, nil)
	closeErr := rpc.Close()
	if callErr == nil || !strings.Contains(callErr.Error(), "23") {
		return errors.Join(fmt.Errorf("expected ACP JSON-RPC EOF from child exit 23, got %v", callErr), closeErr)
	}
	return errors.Join(callErr, closeErr)
}

func runtimeMatrixSuccessfulOutput(role string) string {
	switch role {
	case "plan":
		plan := model.PlanSpecification{
			Objective: "Recover the interrupted attempt", What: "Use the fallback choice", Why: "The first ACP child exited", Benefit: "The role returns a valid result", Where: []string{"src/main.go"},
			AcceptanceCriteria: []string{"The fallback role result is valid"}, Scope: []string{"Retry the configured role queue"}, Exclusions: []string{"Do not alter the worktree"}, Steps: []string{"Retry the first model", "Use the fallback model"},
			RepositoryTestInventory: "None configured: fixture repository configuration inspected", RepositoryBuildInventory: "None configured: fixture repository configuration inspected",
			Requirements: []model.Requirement{{ID: "REQ-RECOVERY", Outcome: "The fallback attempt succeeds", EvidenceMethod: "Inspect persisted attempts"}},
			Checks:       []model.Check{{Name: "recovery", Command: []string{"true"}}}, Units: []model.Unit{{ID: "main", Objective: "Exercise fallback"}},
		}
		encoded, _ := json.Marshal(plan)
		return string(encoded)
	case "review":
		encoded, _ := json.Marshal(model.Review{Summary: "Fallback completed", Findings: []model.Finding{}})
		return string(encoded)
	default:
		return "Fallback completed for " + role
	}
}

func TestProcessCrashRecoveryMatrixAcrossCanonicalRoles(t *testing.T) {
	for _, role := range model.AgentRoles() {
		t.Run(role, func(t *testing.T) {
			fixture, engine := newRuntimeMatrixFixture(t, role)
			fixture.interruptFirstAttempt(t, engine)
			priorOwner := engine.Owner()
			restarted := restartRuntimeMatrixSupervisor(t, engine, fixture.adapter)
			if restarted.Owner() == priorOwner {
				t.Fatal("recreated supervisor reused the prior runtime owner identity")
			}
			fixture.resumeUntilFallback(t, restarted)
			fixture.assertAdapterObservations(t)
			assertRuntimeMatrixAttempts(t, restarted.Store, role, runtimeAttemptExpectations{
				first: fixture.first, fallback: fixture.fallback, priorOwner: priorOwner, currentOwner: restarted.Owner(),
			})
			fixture.assertRestartCount(t, restarted, 4)
		})
	}
}

type runtimeMatrixFixture struct {
	task            model.ExecutionPlan
	project         model.Project
	input           provider.Input
	first, fallback model.AgentChoice
	adapter         *runtimeMatrixAdapter
}

func newRuntimeMatrixFixture(t *testing.T, role string) (runtimeMatrixFixture, *Engine) {
	t.Helper()
	engine, task, cwd := testEngine(t)
	canonical, err := filepath.EvalSymlinks(cwd)
	requireWorkflowSuccess(t, err)
	project, err := engine.Project(context.Background(), task.ProjectID)
	requireWorkflowSuccess(t, err)
	first := model.AgentChoice{Provider: "fake", Model: runtimeMatrixCrashModel, Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}}
	fallback := model.AgentChoice{Provider: "fake", Model: "fallback-model", Thinking: &model.ThinkingChoice{ConfigID: "reasoning", Value: "low"}}
	putFixtureAgent(t, engine.Store, role, model.AgentConfig{Role: role, Chain: []model.AgentChoice{first, fallback}})
	adapter := &runtimeMatrixAdapter{}
	engine.Adapter = func(model.Provider) provider.Adapter { return adapter }
	input := provider.Input{Role: role, CWD: canonical, Prompt: "recover this role"}
	return runtimeMatrixFixture{task: task, project: project, input: input, first: first, fallback: fallback, adapter: adapter}, engine
}

func (fixture runtimeMatrixFixture) interruptFirstAttempt(t *testing.T, engine *Engine) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var backoff []time.Duration
	engine.retryWait = func(waitCtx context.Context, delay time.Duration) error {
		backoff = append(backoff, delay)
		cancel()
		return waitCtx.Err()
	}
	_, err := engine.agent(ctx, fixture.project, fixture.task, fixture.input)
	cancel()
	adapter := fixture.adapter
	if !errors.Is(err, context.Canceled) || adapter.crashes != 1 || !reflect.DeepEqual(backoff, []time.Duration{time.Second}) {
		state := runtimeMatrixPersistedState(t, engine, fixture.project, fixture.task, fixture.input.Role, fixture.first)
		t.Fatalf("first child crash did not persist before supervisor interruption: crashes=%d child_error=%q backoff=%v persisted=%s err=%v", adapter.crashes, adapter.lastCrashError, backoff, state, err)
	}
	fixture.assertRestartCount(t, engine, 1)
}

func restartRuntimeMatrixSupervisor(t *testing.T, engine *Engine, adapter *runtimeMatrixAdapter) *Engine {
	t.Helper()
	storePath := filepath.Join(engine.Root, "state.db")
	requireWorkflowSuccess(t, engine.Store.Close())
	reopened, err := store.Open(storePath)
	requireWorkflowSuccess(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := New(reopened, engine.GitHub, engine.Root)
	restarted.Adapter = func(model.Provider) provider.Adapter { return adapter }
	return restarted
}

func (fixture *runtimeMatrixFixture) resumeUntilFallback(t *testing.T, restarted *Engine) {
	t.Helper()
	var backoff []time.Duration
	restarted.retryWait = func(_ context.Context, delay time.Duration) error {
		backoff = append(backoff, delay)
		if len(backoff) == 1 {
			// Let the persisted remainder of the first cooldown elapse before checking admission.
			time.Sleep(delay)
		}
		return nil
	}
	taskRecord, err := restarted.Store.Get(context.Background(), "execution_plans", fixture.task.ID)
	requireWorkflowSuccess(t, err)
	task, err := store.Decode[model.ExecutionPlan](taskRecord)
	requireWorkflowSuccess(t, err)
	fixture.task = task
	output, err := restarted.agent(context.Background(), fixture.project, task, fixture.input)
	if err != nil || output.SessionID != runtimeMatrixFallbackSession || fixture.adapter.crashes != 4 {
		state := runtimeMatrixPersistedState(t, restarted, fixture.project, task, fixture.input.Role, fixture.first)
		t.Fatalf("recreated supervisor did not exhaust initial plus three restarts before fallback: crashes=%d output=%+v err=%v persisted=%s", fixture.adapter.crashes, output, err, state)
	}
	if len(backoff) != 3 || backoff[0] <= 0 || backoff[0] > time.Second || backoff[1] != 2*time.Second || backoff[2] != 4*time.Second {
		t.Fatalf("resumed backoff=%v, want persisted 1s remainder then 2s and 4s steps", backoff)
	}
}

func (fixture runtimeMatrixFixture) assertRestartCount(t *testing.T, engine *Engine, want int) {
	t.Helper()
	run := &agentRun{Engine: engine, ctx: context.Background(), project: fixture.project, task: fixture.task, input: provider.Input{Role: fixture.input.Role}}
	if restarts, _, err := run.persistedRestartState(fixture.first); err != nil || restarts != want {
		t.Fatalf("persisted restart count=%d, %v; want %d", restarts, err, want)
	}
}

func (fixture runtimeMatrixFixture) assertAdapterObservations(t *testing.T) {
	t.Helper()
	observations := fixture.adapter.observations
	if len(observations) != 5 {
		t.Fatalf("adapter executions=%d, want four crash attempts and one fallback", len(observations))
	}
	for _, observation := range observations {
		if observation.role != fixture.input.Role || observation.profile == "" || observation.profile != observations[0].profile {
			t.Fatalf("role or sandbox profile changed across recovery: %+v", observations)
		}
	}
	for i, observation := range observations[:4] {
		if observation.model != runtimeMatrixCrashModel || observation.thinking == nil || *observation.thinking != *fixture.first.Thinking {
			t.Fatalf("crash choice lost role-specific model/thinking on attempt %d: %+v", i, observation)
		}
	}
	last := observations[4]
	if last.model != fixture.fallback.Model || last.thinking == nil || *last.thinking != *fixture.fallback.Thinking {
		t.Fatalf("role queue fallback lost exact model/thinking: %+v", last)
	}
}

func runtimeMatrixPersistedState(t *testing.T, engine *Engine, project model.Project, task model.ExecutionPlan, role string, choice model.AgentChoice) string {
	t.Helper()
	var summaries []string
	expectedRun := &agentRun{Engine: engine, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: role}}
	expectedEpisode := recoveryEntryID(recoveryEpisodeID(expectedRun), choice)
	for _, kind := range []string{"sessions", "recovery_episodes"} {
		records, err := engine.Store.List(context.Background(), kind)
		if err != nil {
			return fmt.Sprintf("%s list error: %v", kind, err)
		}
		for _, record := range records {
			summary, err := runtimeMatrixRecordSummary(engine, kind, record, expectedEpisode)
			if err != nil {
				return err.Error()
			}
			summaries = append(summaries, summary)
		}
	}
	cooldowns, err := engine.Store.List(context.Background(), "provider_cooldowns")
	if err == nil {
		for _, record := range cooldowns {
			value, decodeErr := store.Decode[model.ProviderCooldown](record)
			if decodeErr == nil {
				summaries = append(summaries, fmt.Sprintf("cooldown(pool=%s attempt=%s until=%s)", value.Pool, value.AttemptID, value.Until))
			}
		}
	}
	return strings.Join(summaries, "; ")
}

type runtimeAttemptExpectations struct {
	first, fallback          model.AgentChoice
	priorOwner, currentOwner string
}

func assertRuntimeMatrixAttempts(t *testing.T, database *store.Store, role string, expected runtimeAttemptExpectations) {
	t.Helper()
	attempts := storedRuntimeMatrixAttempts(t, database, role)
	if len(attempts) != 5 {
		t.Fatalf("persisted role attempts=%d, want five: %+v", len(attempts), attempts)
	}
	for index, attempt := range attempts[:4] {
		assertRuntimeCrashAttempt(t, attempt, index, expected)
	}
	assertRuntimeFallbackAttempt(t, attempts[0], attempts[4], expected)
}

func assertRuntimeCrashAttempt(t *testing.T, attempt model.Attempt, index int, expected runtimeAttemptExpectations) {
	t.Helper()
	if attempt.Provider != expected.first.Provider || attempt.Model != expected.first.Model || attempt.RestartCount != index || attempt.Status != "failed" || attempt.ExecutionProfile == "" {
		t.Fatalf("persisted crash attempt %d lost choice or restart count: %+v", index, attempt)
	}
	if attempt.Thinking == nil || *attempt.Thinking != *expected.first.Thinking {
		t.Fatalf("persisted crash attempt %d lost thinking: %+v", index, attempt.Thinking)
	}
	wantOwner := expected.currentOwner
	if index == 0 {
		wantOwner = expected.priorOwner
	}
	if attempt.Owner != wantOwner {
		t.Fatalf("crash attempt %d owner=%q, want supervisor owner %q", index, attempt.Owner, wantOwner)
	}
	assertQueueEntry(t, attempt, 1, 2)
}

func assertRuntimeFallbackAttempt(t *testing.T, first, last model.Attempt, expected runtimeAttemptExpectations) {
	t.Helper()
	if last.Provider != expected.fallback.Provider || last.Model != expected.fallback.Model || last.RestartCount != 0 || last.Status != "complete" || last.Success == nil || !*last.Success {
		t.Fatalf("persisted role fallback attempt lost queue authority: %+v", last)
	}
	if last.Thinking == nil || *last.Thinking != *expected.fallback.Thinking || last.ExecutionProfile != first.ExecutionProfile {
		t.Fatalf("fallback thinking or sandbox profile changed: first=%+v fallback=%+v", first, last)
	}
	if last.Owner != expected.currentOwner {
		t.Fatalf("fallback owner=%q, want recreated supervisor owner %q", last.Owner, expected.currentOwner)
	}
	assertQueueEntry(t, last, 2, 2)
}

func storedRuntimeMatrixAttempts(t *testing.T, database *store.Store, role string) []model.Attempt {
	t.Helper()
	records, err := database.List(context.Background(), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	attempts := make([]model.Attempt, 0, 5)
	for _, record := range records {
		attempt, decodeErr := store.Decode[model.Attempt](record)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if attempt.Role == role {
			attempts = append(attempts, attempt)
		}
	}
	sort.Slice(attempts, func(i, j int) bool { return attempts[i].StartedAt < attempts[j].StartedAt })
	return attempts
}

func assertQueueEntry(t *testing.T, attempt model.Attempt, position, length int) {
	t.Helper()
	encoded, err := json.Marshal(attempt)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(encoded, &saved); err != nil {
		t.Fatal(err)
	}
	savedPosition, positionOK := saved["queue_position"].(float64)
	savedLength, lengthOK := saved["queue_length"].(float64)
	if !positionOK || !lengthOK || int(savedPosition) != position || int(savedLength) != length {
		t.Fatalf("persisted queue entry for %s = %v/%v, want %d/%d", attempt.AttemptID, saved["queue_position"], saved["queue_length"], position, length)
	}
}

func runtimeMatrixRecordSummary(engine *Engine, kind string, record model.Record, expectedEpisode string) (string, error) {
	if kind == "sessions" {
		state, decodeErr := store.Decode[model.Attempt](record)
		if decodeErr != nil {
			return "", fmt.Errorf("%s decode error: %v", kind, decodeErr)
		}
		events, eventErr := engine.Store.AttemptEvents(context.Background(), state.AttemptID, 0, 100)
		var kinds []string
		if eventErr == nil {
			for _, event := range events {
				kinds = append(kinds, event.Kind)
			}
		}
		identityMatches := state.AttemptID == state.ExecutionID && state.ExecutionGeneration == 1 && state.Owner == engine.Owner() && state.RecoveryEpisodeID == expectedEpisode
		return fmt.Sprintf("session(attempt=%s execution=%s generation=%d owner=%s episode=%s expected_owner=%s expected_episode=%s identity_matches=%t role=%s model=%s restart=%d status=%s success=%v error=%q process=%s events=%v event_error=%v)", state.AttemptID, state.ExecutionID, state.ExecutionGeneration, state.Owner, state.RecoveryEpisodeID, engine.Owner(), expectedEpisode, identityMatches, state.Role, state.Model, state.RestartCount, state.Status, state.Success, state.Error, state.ProcessState, kinds, eventErr), nil
	}
	episode, decodeErr := store.Decode[model.RecoveryEpisode](record)
	if decodeErr != nil {
		return "", fmt.Errorf("%s decode error: %v", kind, decodeErr)
	}
	return fmt.Sprintf("episode(role=%s model=%s restart=%d status=%s failure=%s)", episode.Role, episode.Model, episode.RestartCount, episode.Status, episode.FailureKind), nil
}
