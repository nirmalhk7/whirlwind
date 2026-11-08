package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestUnfinishedToolActionRequiresOutcomeReconciliation(t *testing.T) {
	events := []model.AttemptEvent{{
		Sequence: 1,
		Kind:     "whirlwind/action_started",
		Metadata: json.RawMessage(`{"action_id":"rpc-1","method":"fs/write_text_file"}`),
	}}
	if actions := unresolvedToolActions(events); len(actions) != 1 || actions[0].ID != "rpc-1" {
		t.Fatalf("unfinished workspace write was not retained as uncertain: %+v", actions)
	}
}

func TestCompletedToolActionDoesNotBlockRecovery(t *testing.T) {
	events := []model.AttemptEvent{
		{Sequence: 1, Kind: "whirlwind/action_started", Metadata: json.RawMessage(`{"action_id":"rpc-1","method":"fs/write_text_file"}`)},
		{Sequence: 2, Kind: "whirlwind/action_result", Metadata: json.RawMessage(`{"action_id":"rpc-1","method":"fs/write_text_file","status":"completed"}`)},
	}
	if actions := unresolvedToolActions(events); len(actions) != 0 {
		t.Fatalf("completed workspace write remained uncertain: %+v", actions)
	}
}

func TestTerminalActionRemainsUncertainUntilVerifiedExit(t *testing.T) {
	events := []model.AttemptEvent{
		{Sequence: 1, Kind: "whirlwind/action_started", Metadata: json.RawMessage(`{"action_id":"rpc-1","method":"terminal/create"}`)},
		{Sequence: 2, Kind: "whirlwind/action_result", Metadata: json.RawMessage(`{"action_id":"rpc-1","method":"terminal/create","status":"completed","terminal_id":"terminal-1"}`)},
	}
	if actions := unresolvedToolActions(events); len(actions) != 1 {
		t.Fatalf("running terminal was treated as complete before exit: %+v", actions)
	}
	events = append(events, model.AttemptEvent{Sequence: 3, Kind: "whirlwind/terminal/exit", Metadata: json.RawMessage(`{"terminalId":"terminal-1","exitCode":0,"cleanup_verified":true}`)})
	if actions := unresolvedToolActions(events); len(actions) != 0 {
		t.Fatalf("verified terminal exit did not resolve the action: %+v", actions)
	}
}

func TestFailedTerminalCleanupRemainsUncertain(t *testing.T) {
	events := []model.AttemptEvent{
		{Sequence: 1, Kind: "whirlwind/action_started", Metadata: json.RawMessage(`{"action_id":"rpc-1","method":"terminal/create"}`)},
		{Sequence: 2, Kind: "whirlwind/action_result", Metadata: json.RawMessage(`{"action_id":"rpc-1","method":"terminal/create","status":"completed","terminal_id":"terminal-1"}`)},
		{Sequence: 3, Kind: "whirlwind/terminal/exit", Metadata: json.RawMessage(`{"terminalId":"terminal-1","exitCode":0,"cleanup_verified":false}`)},
	}
	if actions := unresolvedToolActions(events); len(actions) != 1 {
		t.Fatalf("unverified terminal cleanup was treated as safe: %+v", actions)
	}
}

func TestUnfinishedPersistedToolActionBlocksCrossProviderReplacement(t *testing.T) {
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	prior := model.Attempt{AttemptID: "prior-action", Task: task.ID, ProjectID: task.ProjectID, PlanVersion: task.PlanVersion, Role: "code", Purpose: "primary", UnitID: "unit-1", Provider: "first", Status: "interrupted"}
	if err := e.Store.Put(context.Background(), "sessions", prior.AttemptID, prior, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.Capture(context.Background(), prior.AttemptID, "whirlwind/action_started", "application/json", []byte(`{"action_id":"rpc-7","method":"fs/write_text_file"}`), store.MessagePayloadLimit, false); err != nil {
		t.Fatal(err)
	}
	run := &agentRun{Engine: e, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: "code", Purpose: "primary", UnitID: "unit-1", CWD: cwd}}
	err = run.reconcilePriorToolActions(context.Background())
	if !errors.Is(err, ErrActionOutcomeUncertain) || !strings.Contains(err.Error(), "rpc-7") {
		t.Fatalf("replacement did not pause on uncertain persisted write: %v", err)
	}
}

func TestCompletedPersistedToolActionAllowsReplacement(t *testing.T) {
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	prior := model.Attempt{AttemptID: "prior-action-complete", Task: task.ID, ProjectID: task.ProjectID, PlanVersion: task.PlanVersion, Role: "code", Purpose: "primary", UnitID: "unit-1", Provider: "first", Status: "failed"}
	if err := e.Store.Put(context.Background(), "sessions", prior.AttemptID, prior, 0); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ kind, body string }{
		{"whirlwind/action_started", `{"action_id":"rpc-8","method":"fs/write_text_file"}`},
		{"whirlwind/action_result", `{"action_id":"rpc-8","method":"fs/write_text_file","status":"completed"}`},
	} {
		if _, err := e.Store.Capture(context.Background(), prior.AttemptID, item.kind, "application/json", []byte(item.body), store.MessagePayloadLimit, false); err != nil {
			t.Fatal(err)
		}
	}
	run := &agentRun{Engine: e, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: "code", Purpose: "primary", UnitID: "unit-1", CWD: cwd}}
	if err := run.reconcilePriorToolActions(context.Background()); err != nil {
		t.Fatalf("completed persisted action blocked replacement: %v", err)
	}
}

func TestUnfinishedToolActionBlocksRecoveryForEveryConfiguredRole(t *testing.T) {
	for _, role := range model.AgentRoles() {
		t.Run(role, func(t *testing.T) {
			e, task, cwd := testEngine(t)
			project, err := e.Project(context.Background(), task.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			attemptID := "prior-" + role
			attempt := model.Attempt{AttemptID: attemptID, Task: task.ID, ProjectID: task.ProjectID, PlanVersion: task.PlanVersion, Role: role, Purpose: "primary", UnitID: "unit-" + role, Status: "interrupted"}
			if err := e.Store.Put(context.Background(), "sessions", attemptID, attempt, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Store.Capture(context.Background(), attemptID, "whirlwind/action_started", "application/json", []byte(`{"action_id":"rpc-"`+role+`,"method":"terminal/create"}`), store.MessagePayloadLimit, false); err != nil {
				t.Fatal(err)
			}
			run := &agentRun{Engine: e, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: role, Purpose: "primary", UnitID: "unit-" + role, CWD: cwd}}
			if err := run.reconcilePriorToolActions(context.Background()); !errors.Is(err, ErrActionOutcomeUncertain) {
				t.Fatalf("role recovery did not preserve uncertain terminal outcome: %v", err)
			}
		})
	}
}
