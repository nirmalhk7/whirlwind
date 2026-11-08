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

const runtimeMatrixCrashModel = "crashing-acp"

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
	return provider.Output{Text: runtimeMatrixSuccessfulOutput(input.Role), SessionID: "fallback-session"}, nil
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
		plan := model.Plan{
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
	roles := model.AgentRoles()
	for _, role := range roles {
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
			firstChoice := model.AgentChoice{Provider: "fake", Model: runtimeMatrixCrashModel, Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}}
			fallbackChoice := model.AgentChoice{Provider: "fake", Model: "fallback-model", Thinking: &model.ThinkingChoice{ConfigID: "reasoning", Value: "low"}}
			putFixtureAgent(t, engine.Store, role, model.AgentConfig{Role: role, Chain: []model.AgentChoice{firstChoice, fallbackChoice}})
			adapter := &runtimeMatrixAdapter{}
			engine.Adapter = func(model.Provider) provider.Adapter { return adapter }

			ctx, cancel := context.WithCancel(context.Background())
			var firstBackoff []time.Duration
			engine.retryWait = func(waitCtx context.Context, delay time.Duration) error {
				firstBackoff = append(firstBackoff, delay)
				cancel()
				return waitCtx.Err()
			}
			_, firstErr := engine.agent(ctx, project, task, provider.Input{Role: role, CWD: cwd, Prompt: "recover this role"})
			cancel()
			if !errors.Is(firstErr, context.Canceled) || adapter.crashes != 1 || !reflect.DeepEqual(firstBackoff, []time.Duration{time.Second}) {
				t.Fatalf("first child crash did not persist before supervisor interruption: crashes=%d child_error=%q backoff=%v persisted=%s err=%v", adapter.crashes, adapter.lastCrashError, firstBackoff, runtimeMatrixPersistedState(t, engine, project, task, role, firstChoice), firstErr)
			}

			firstRun := &agentRun{Engine: engine, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: role}}
			if restarts, _, err := firstRun.persistedRestartState(firstChoice); err != nil || restarts != 1 {
				t.Fatalf("persisted restart count after first child crash=%d, %v; want 1", restarts, err)
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
					// The first wait is the persisted remainder from the interrupted
					// 1s cooldown. Let that deadline elapse before checking admission.
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
			output, err := restarted.agent(context.Background(), project, currentTask, provider.Input{Role: role, CWD: cwd, Prompt: "recover this role"})
			if err != nil || output.SessionID != "fallback-session" || adapter.crashes != 4 {
				state := runtimeMatrixPersistedState(t, restarted, project, currentTask, role, firstChoice)
				t.Fatalf("recreated supervisor did not exhaust initial plus three restarts before fallback: crashes=%d output=%+v err=%v persisted=%s", adapter.crashes, output, err, state)
			}
			if len(resumedBackoff) != 3 || resumedBackoff[0] <= 0 || resumedBackoff[0] > time.Second || resumedBackoff[1] != 2*time.Second || resumedBackoff[2] != 4*time.Second {
				t.Fatalf("resumed backoff=%v, want persisted 1s remainder then 2s and 4s steps", resumedBackoff)
			}
			if len(adapter.observations) != 5 {
				t.Fatalf("adapter executions=%d, want four crash attempts and one fallback", len(adapter.observations))
			}
			for _, observation := range adapter.observations {
				if observation.role != role || observation.profile == "" || observation.profile != adapter.observations[0].profile {
					t.Fatalf("role or sandbox profile changed across recovery: %+v", adapter.observations)
				}
			}
			for i := 0; i < 4; i++ {
				observation := adapter.observations[i]
				if observation.model != runtimeMatrixCrashModel || observation.thinking == nil || *observation.thinking != *firstChoice.Thinking {
					t.Fatalf("crash choice lost role-specific model/thinking on attempt %d: %+v", i, observation)
				}
			}
			fallbackObservation := adapter.observations[4]
			if fallbackObservation.model != fallbackChoice.Model || fallbackObservation.thinking == nil || *fallbackObservation.thinking != *fallbackChoice.Thinking {
				t.Fatalf("role queue fallback lost exact model/thinking: %+v", fallbackObservation)
			}
			assertRuntimeMatrixAttempts(t, reopened, role, firstChoice, fallbackChoice, priorOwner, restarted.Owner())
			restartedRun := &agentRun{Engine: restarted, ctx: context.Background(), project: project, task: currentTask, input: provider.Input{Role: role}}
			if restarts, _, err := restartedRun.persistedRestartState(firstChoice); err != nil || restarts != 4 {
				t.Fatalf("restart budget after four child exits=%d, %v; want persisted exhausted count 4", restarts, err)
			}
		})
	}
}

func runtimeMatrixPersistedState(t *testing.T, engine *Engine, project model.Project, task model.Task, role string, choice model.AgentChoice) string {
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
			if kind == "sessions" {
				state, decodeErr := store.Decode[model.Attempt](record)
				if decodeErr != nil {
					return fmt.Sprintf("%s decode error: %v", kind, decodeErr)
				}
				events, eventErr := engine.Store.AttemptEvents(context.Background(), state.AttemptID, 0, 100)
				var kinds []string
				if eventErr == nil {
					for _, event := range events {
						kinds = append(kinds, event.Kind)
					}
				}
				identityMatches := state.AttemptID == state.ExecutionID && state.ExecutionGeneration == 1 && state.Owner == engine.Owner() && state.RecoveryEpisodeID == expectedEpisode
				summaries = append(summaries, fmt.Sprintf("session(attempt=%s execution=%s generation=%d owner=%s episode=%s expected_owner=%s expected_episode=%s identity_matches=%t role=%s model=%s restart=%d status=%s success=%v error=%q process=%s events=%v event_error=%v)", state.AttemptID, state.ExecutionID, state.ExecutionGeneration, state.Owner, state.RecoveryEpisodeID, engine.Owner(), expectedEpisode, identityMatches, state.Role, state.Model, state.RestartCount, state.Status, state.Success, state.Error, state.ProcessState, kinds, eventErr))
				continue
			}
			episode, decodeErr := store.Decode[model.RecoveryEpisode](record)
			if decodeErr != nil {
				return fmt.Sprintf("%s decode error: %v", kind, decodeErr)
			}
			summaries = append(summaries, fmt.Sprintf("episode(role=%s model=%s restart=%d status=%s failure=%s)", episode.Role, episode.Model, episode.RestartCount, episode.Status, episode.FailureKind))
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

func assertRuntimeMatrixAttempts(t *testing.T, database *store.Store, role string, first, fallback model.AgentChoice, priorOwner, restartedOwner string) {
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
	if len(attempts) != 5 {
		t.Fatalf("persisted role attempts=%d, want five: %+v", len(attempts), attempts)
	}
	for index := 0; index < 4; index++ {
		attempt := attempts[index]
		if attempt.Provider != first.Provider || attempt.Model != first.Model || attempt.RestartCount != index || attempt.Status != "failed" || attempt.ExecutionProfile == "" {
			t.Fatalf("persisted crash attempt %d lost choice or restart count: %+v", index, attempt)
		}
		if attempt.Thinking == nil || *attempt.Thinking != *first.Thinking {
			t.Fatalf("persisted crash attempt %d lost thinking: %+v", index, attempt.Thinking)
		}
		wantOwner := restartedOwner
		if index == 0 {
			wantOwner = priorOwner
		}
		if attempt.Owner != wantOwner {
			t.Fatalf("crash attempt %d owner=%q, want supervisor owner %q", index, attempt.Owner, wantOwner)
		}
		assertQueueEntry(t, attempt, 1, 2)
	}
	last := attempts[4]
	if last.Provider != fallback.Provider || last.Model != fallback.Model || last.RestartCount != 0 || last.Status != "complete" || last.Success == nil || !*last.Success {
		t.Fatalf("persisted role fallback attempt lost queue authority: %+v", last)
	}
	if last.Thinking == nil || *last.Thinking != *fallback.Thinking || last.ExecutionProfile != attempts[0].ExecutionProfile {
		t.Fatalf("fallback thinking or sandbox profile changed: first=%+v fallback=%+v", attempts[0], last)
	}
	if last.Owner != restartedOwner {
		t.Fatalf("fallback owner=%q, want recreated supervisor owner %q", last.Owner, restartedOwner)
	}
	assertQueueEntry(t, last, 2, 2)
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
