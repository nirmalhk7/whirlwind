package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const malformedPlannerMatrixModel = "malformed-plan-output"

type malformedPlannerMatrixAdapter struct {
	malformed int
	fallbacks int
}

func (a *malformedPlannerMatrixAdapter) Run(_ context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
	if input.Model == malformedPlannerMatrixModel {
		a.malformed++
		return provider.Output{Text: "{"}, nil
	}
	if input.SessionID != "" {
		return provider.Output{}, fmt.Errorf("session crossed malformed-output fallback: %q", input.SessionID)
	}
	a.fallbacks++
	return provider.Output{Text: runtimeMatrixSuccessfulOutput("plan"), SessionID: "valid-planner-fallback"}, nil
}

func TestMalformedPlannerRecoveryBudgetSurvivesSupervisorReopen(t *testing.T) {
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
	firstChoice := model.AgentChoice{Provider: "fake", Model: malformedPlannerMatrixModel, Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}}
	fallbackChoice := model.AgentChoice{Provider: "fake", Model: "valid-plan-fallback", Thinking: &model.ThinkingChoice{ConfigID: "reasoning", Value: "low"}}
	putFixtureAgent(t, engine.Store, "plan", model.AgentConfig{Role: "plan", Chain: []model.AgentChoice{firstChoice, fallbackChoice}})
	adapter := &malformedPlannerMatrixAdapter{}
	engine.Adapter = func(model.Provider) provider.Adapter { return adapter }

	ctx, cancel := context.WithCancel(context.Background())
	var firstBackoff []time.Duration
	engine.retryWait = func(waitCtx context.Context, delay time.Duration) error {
		firstBackoff = append(firstBackoff, delay)
		cancel()
		return waitCtx.Err()
	}
	_, firstErr := engine.agent(ctx, project, task, provider.Input{Role: "plan", CWD: cwd, Prompt: "produce a valid plan"})
	cancel()
	if !errors.Is(firstErr, context.Canceled) || adapter.malformed != 1 || !reflect.DeepEqual(firstBackoff, []time.Duration{time.Second}) {
		t.Fatalf("first malformed planner response was not durably interrupted before retry: malformed=%d waits=%v err=%v", adapter.malformed, firstBackoff, firstErr)
	}
	assertMalformedPlannerEpisode(t, engine, task, firstChoice, 1)

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
	output, err := restarted.agent(context.Background(), project, currentTask, provider.Input{Role: "plan", CWD: cwd, Prompt: "produce a valid plan"})
	if err != nil || output.SessionID != "valid-planner-fallback" || adapter.malformed != 4 || adapter.fallbacks != 1 {
		t.Fatalf("malformed planner budget did not retain three restarts before fallback: malformed=%d fallbacks=%d output=%+v err=%v state=%s", adapter.malformed, adapter.fallbacks, output, err, runtimeMatrixPersistedState(t, restarted, project, currentTask, "plan", firstChoice))
	}
	if len(resumedBackoff) != 3 || resumedBackoff[0] <= 0 || resumedBackoff[0] > time.Second || resumedBackoff[1] != 2*time.Second || resumedBackoff[2] != 4*time.Second {
		t.Fatalf("malformed-output resumed backoff=%v, want persisted 1s remainder then 2s and 4s", resumedBackoff)
	}
	assertRuntimeMatrixAttempts(t, reopened, "plan", firstChoice, fallbackChoice, priorOwner, restarted.Owner())
	assertMalformedPlannerEpisode(t, restarted, currentTask, firstChoice, 4)
}

func assertMalformedPlannerEpisode(t *testing.T, engine *Engine, task model.Task, choice model.AgentChoice, expectedRestarts int) {
	t.Helper()
	run := &agentRun{Engine: engine, ctx: context.Background(), project: model.Project{ID: "p"}, task: task, input: provider.Input{Role: "plan"}}
	entryID := recoveryEntryID(recoveryEpisodeID(run), choice)
	record, err := engine.Store.Get(context.Background(), "recovery_episodes", entryID)
	if err != nil {
		t.Fatalf("malformed planner recovery episode was not persisted: %v", err)
	}
	episode, err := store.Decode[model.RecoveryEpisode](record)
	if err != nil {
		t.Fatal(err)
	}
	if episode.FailureKind != string(provider.FailureTransient) || episode.RestartCount != expectedRestarts-1 || episode.Status != "failed" {
		t.Fatalf("malformed planner episode lost typed failure or bounded counter: %+v; expected attempts=%d", episode, expectedRestarts)
	}
}
