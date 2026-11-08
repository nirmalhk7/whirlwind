package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const persistedActionStartedKind = "whirlwind/action_started"

func TestActionResultPersistenceFailureSurvivesSupervisorRestart(t *testing.T) {
	e, task, cwd := testEngine(t)
	project := loadActionTestProject(t, e, task)
	const attemptID = "durable-action-failure"
	attempt, cancel := startPersistedActionAttempt(t, e, task, project, cwd, attemptID)
	defer cancel()
	effect := filepath.Join(cwd, "action-effect.txt")
	installActionResultFailure(t, e.Store)
	effectCount := runActionUntilResultFailure(t, e.Store, attempt, attemptID, cwd, effect)
	assertActionResultFailure(t, attempt, effect, effectCount)
	interruptAttempt(t, e.Store, attemptID)

	restarted, reopened := reopenActionTestSupervisor(t, e)
	assertStartedActionBlocksReplacement(t, restarted, task, project, cwd, "rpc-7")
	assertOnlyActionStartedPersisted(t, reopened, attemptID)
	if effectCount != 1 {
		t.Fatalf("replacement replayed handler side effect: count=%d", effectCount)
	}
}

func loadActionTestProject(t *testing.T, e *Engine, task model.Task) model.Project {
	t.Helper()
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return project
}

func installActionResultFailure(t *testing.T, database *store.Store) {
	t.Helper()
	_, err := database.DB.Exec(`CREATE TRIGGER fail_action_result BEFORE INSERT ON attempt_events
		WHEN NEW.kind='whirlwind/action_result'
		BEGIN SELECT RAISE(ABORT,'injected action result persistence failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
}

func runActionUntilResultFailure(t *testing.T, database *store.Store, attempt *agentAttempt, attemptID, cwd, effect string) int {
	t.Helper()
	var effects int
	resultObserved := make(chan struct{}, 1)
	rpc, err := provider.Start(attempt.ctx, actionFailureProviderCommand(), cwd, actionSideEffectHandler(t, database, attemptID, effect, &effects), func(kind string, payload any) {
		attempt.event(kind, payload)
		if kind == "whirlwind/action_result" {
			resultObserved <- struct{}{}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForActionResult(t, rpc, resultObserved)
	if err := rpc.Close(); err != nil {
		t.Fatalf("close provider after persistence failure: %v", err)
	}
	return effects
}

func actionFailureProviderCommand() []string {
	return []string{"sh", "-c", `printf '%s\n' '{"jsonrpc":"2.0","id":"rpc-7","method":"fs/write_text_file","params":{}}'; sleep 60`}
}

func actionSideEffectHandler(t *testing.T, database *store.Store, attemptID, effect string, count *int) provider.Handler {
	t.Helper()
	return func(_ context.Context, _ provider.Message) (any, error) {
		events, err := database.AttemptActionEvents(context.Background(), attemptID)
		if err != nil {
			return nil, err
		}
		if len(events) != 1 || events[0].Kind != persistedActionStartedKind {
			return nil, fmt.Errorf("handler ran without durable action_started: %+v", events)
		}
		*count++
		if err := os.WriteFile(effect, []byte("written once"), 0600); err != nil {
			return nil, err
		}
		return map[string]bool{"written": true}, nil
	}
}

func waitForActionResult(t *testing.T, rpc *provider.RPC, observed <-chan struct{}) {
	t.Helper()
	select {
	case <-observed:
	case <-time.After(10 * time.Second):
		_ = rpc.Close()
		t.Fatal("provider did not reach durable action-result persistence")
	}
}

func assertActionResultFailure(t *testing.T, attempt *agentAttempt, effect string, count int) {
	t.Helper()
	attempt.mu.Lock()
	persistErr := attempt.eventErr
	attempt.mu.Unlock()
	if !errors.Is(persistErr, store.ErrPersistence) || !strings.Contains(persistErr.Error(), "injected action result persistence failure") {
		t.Fatalf("action result persistence failure not retained: %v", persistErr)
	}
	if count != 1 {
		t.Fatalf("handler side effect count=%d, want 1", count)
	}
	data, err := os.ReadFile(effect)
	if err != nil || string(data) != "written once" {
		t.Fatalf("handler side effect missing: data=%q err=%v", data, err)
	}
}

func reopenActionTestSupervisor(t *testing.T, prior *Engine) (*Engine, *store.Store) {
	t.Helper()
	if err := prior.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(filepath.Join(prior.Root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	return New(reopened, fakeGitHub{}, prior.Root), reopened
}

func TestCompletedActionResultSurvivesSupervisorRestartAndAllowsPreparation(t *testing.T) {
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	const attemptID = "durable-action-complete"
	prior := persistedActionAttempt(task, project, cwd, attemptID)
	prior.RuntimeKind = "local"
	prior.Status = "completed"
	prior.Finished = true
	if err := e.Store.Put(context.Background(), "sessions", attemptID, prior, 0); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ kind, body string }{
		{persistedActionStartedKind, `{"action_id":"rpc-8","method":"fs/write_text_file"}`},
		{"whirlwind/action_result", `{"action_id":"rpc-8","method":"fs/write_text_file","status":"completed"}`},
	} {
		if _, err := e.Store.Capture(context.Background(), attemptID, item.kind, "application/json", []byte(item.body), store.MessagePayloadLimit, false); err != nil {
			t.Fatal(err)
		}
	}

	storePath := filepath.Join(e.Root, "state.db")
	if err := e.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := New(reopened, fakeGitHub{}, e.Root)
	run := recoveryActionRun(restarted, task, project, cwd)
	prepared, err := run.prepareNewAttempt(model.Provider{ID: "fake", Kind: "acp", Available: true, Capabilities: model.AgentRoles()})
	if err != nil {
		t.Fatalf("completed durable action blocked replacement preparation: %v", err)
	}
	if prepared == nil || prepared.id == "" {
		t.Fatal("replacement attempt was not prepared after completed action outcome")
	}
}

func startPersistedActionAttempt(t *testing.T, e *Engine, task model.Task, project model.Project, cwd, id string) (*agentAttempt, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	state := persistedActionAttempt(task, project, cwd, id)
	state.ExecutionID = id
	state.ExecutionGeneration = 1
	state.Status = "running"
	state.Owner = "test-supervisor"
	state.LeaseExpiresAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	state.DeadlineAt = time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)
	if err := e.Store.Put(context.Background(), "sessions", id, state, 0); err != nil {
		t.Fatal(err)
	}
	run := recoveryActionRun(e, task, project, cwd)
	run.ctx = ctx
	attempt := &agentAttempt{agentRun: run, id: id, state: state, cancel: cancel, chosen: model.Provider{ID: "fake"}}
	return attempt, cancel
}

func persistedActionAttempt(task model.Task, project model.Project, cwd, id string) model.Attempt {
	return model.Attempt{
		AttemptID: id, Task: task.ID, ProjectID: project.ID,
		PlanVersion: task.PlanVersion, Role: "plan", Purpose: "primary", UnitID: "unit-1",
		Provider: "fake", CWD: cwd, StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func recoveryActionRun(e *Engine, task model.Task, project model.Project, cwd string) *agentRun {
	return &agentRun{
		Engine: e, ctx: context.Background(), task: task, project: project,
		input: provider.Input{Role: "plan", Purpose: "primary", UnitID: "unit-1", CWD: cwd},
	}
}

func interruptAttempt(t *testing.T, database *store.Store, id string) {
	t.Helper()
	record, err := database.Get(context.Background(), "sessions", id)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Decode[model.Attempt](record)
	if err != nil {
		t.Fatal(err)
	}
	state.Status = "interrupted"
	state.Finished = true
	if err := database.Put(context.Background(), "sessions", id, state, record.Version); err != nil {
		t.Fatal(err)
	}
}

func assertStartedActionBlocksReplacement(t *testing.T, e *Engine, task model.Task, project model.Project, cwd, actionID string) {
	t.Helper()
	run := recoveryActionRun(e, task, project, cwd)
	_, err := run.newAttempt(model.Provider{ID: "replacement", Kind: "acp", Available: true, Capabilities: model.AgentRoles()})
	if !errors.Is(err, ErrActionOutcomeUncertain) || !strings.Contains(err.Error(), actionID) {
		t.Fatalf("replacement attempt was not blocked by uncertain durable action: %v", err)
	}
	records, err := e.Store.List(context.Background(), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("uncertain action created replacement session; sessions=%d", len(records))
	}
}

func assertOnlyActionStartedPersisted(t *testing.T, database *store.Store, attemptID string) {
	t.Helper()
	events, err := database.AttemptActionEvents(context.Background(), attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != persistedActionStartedKind {
		t.Fatalf("expected only durable action_started after failed result commit, got %+v", events)
	}
}
