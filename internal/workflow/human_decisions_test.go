package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestAnswerQuestionResumesBoundRoleAndPreservesHistory(t *testing.T) {
	e, task, _ := testEngine(t)
	questionID, questionVersion := seedHumanDecision(t, e, task, "review", "review", "question")
	answers := map[string]string{"choice": "retain current scope"}
	if err := e.AnswerQuestionSet(context.Background(), questionID, questionVersion, answers, false); err != nil {
		t.Fatal(err)
	}
	assertResumedDecision(t, e, task.ID, "review", 1)
	record, err := e.Store.Get(context.Background(), "questions", questionID)
	if err != nil {
		t.Fatal(err)
	}
	set, err := store.Decode[model.QuestionSet](record)
	if err != nil || set.Status != "answered" || set.Answers["choice"] != answers["choice"] {
		t.Fatalf("answer not durably recorded: %+v %v", set, err)
	}
}

func TestPermissionDecisionResumesOriginatingPlannerWithoutResettingRetries(t *testing.T) {
	e, task, _ := testEngine(t)
	permissionID, permissionVersion := seedHumanDecision(t, e, task, "planning", "plan", "permission")
	if err := e.DecidePermission(context.Background(), permissionID, permissionVersion, "allow"); err != nil {
		t.Fatal(err)
	}
	assertResumedDecision(t, e, task.ID, "planning", 1)
	record, err := e.Store.Get(context.Background(), "permissions", permissionID)
	if err != nil {
		t.Fatal(err)
	}
	var permission struct {
		Decision string `json:"decision"`
	}
	if err := storeDecode(record, &permission); err != nil || permission.Decision != "allow" {
		t.Fatalf("permission response not durable: %+v %v", permission, err)
	}
}

func TestHumanDecisionReceiptCommitsWithTransitionAndOnlyOnSuccess(t *testing.T) {
	e, task, _ := testEngine(t)
	questionID, questionVersion := seedHumanDecision(t, e, task, "review", "review", "question")
	ctx := context.Background()
	receipt := &store.Receipt{Key: "decision-key", Fingerprint: "decision-fingerprint", Status: 200, Body: []byte(`{"status":"answered"}`)}
	if err := e.AnswerQuestionSetWithReceipt(ctx, questionID, questionVersion, map[string]string{"choice": "answer"}, false, receipt); err != nil {
		t.Fatal(err)
	}
	status, body, err := e.Store.Idempotent(ctx, receipt.Key, receipt.Fingerprint)
	if err != nil || status != receipt.Status || string(body) != string(receipt.Body) {
		t.Fatalf("successful response receipt was not committed: status=%d body=%s err=%v", status, body, err)
	}
	staleID, staleVersion := seedHumanDecision(t, e, task, "review", "review", "question")
	stale := &store.Receipt{Key: "stale-decision-key", Fingerprint: "stale-fingerprint", Status: 200, Body: []byte(`{"status":"answered"}`)}
	if err := e.AnswerQuestionSetWithReceipt(ctx, staleID, staleVersion-1, map[string]string{"choice": "answer"}, false, stale); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale response was accepted: %v", err)
	}
	status, _, err = e.Store.Idempotent(ctx, stale.Key, stale.Fingerprint)
	if err != nil || status != 0 {
		t.Fatalf("failed response wrote an idempotency receipt: status=%d err=%v", status, err)
	}
}

func TestLegacyPendingDecisionSurvivesSupervisorRecovery(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	questionID, questionVersion := seedHumanDecision(t, e, task, "review", "review", "question")
	questionRecord, err := e.Store.Get(ctx, "questions", questionID)
	if err != nil {
		t.Fatal(err)
	}
	question, err := store.Decode[model.QuestionSet](questionRecord)
	if err != nil {
		t.Fatal(err)
	}
	attemptRecord, err := e.Store.Get(ctx, "sessions", question.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.Decode[model.Attempt](attemptRecord)
	if err != nil {
		t.Fatal(err)
	}
	const originalDeadline = "2026-11-06T20:00:00Z"
	attempt.Status, attempt.DeadlineAt = "paused", originalDeadline
	if err := e.Store.Put(ctx, "sessions", question.AttemptID, attempt, attemptRecord.Version); err != nil {
		t.Fatal(err)
	}
	taskRecord, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyTask, err := store.Decode[model.Task](taskRecord)
	if err != nil {
		t.Fatal(err)
	}
	legacyTask.Wait.AttemptID = ""
	legacyTask.Recoveries = 2
	if err := e.Store.Put(ctx, "tasks", task.ID, legacyTask, taskRecord.Version); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.RecoverInterrupted(ctx, e.owner); err != nil {
		t.Fatal(err)
	}
	recoveredAttemptRecord, err := e.Store.Get(ctx, "sessions", question.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	recoveredAttempt, err := store.Decode[model.Attempt](recoveredAttemptRecord)
	if err != nil || recoveredAttempt.DeadlineAt != originalDeadline || recoveredAttempt.Status != "paused" {
		t.Fatalf("legacy paused attempt changed during recovery: %+v %v", recoveredAttempt, err)
	}
	if err := e.AnswerQuestionSet(ctx, questionID, questionVersion, map[string]string{"choice": "continue reviewed scope"}, false); err != nil {
		t.Fatalf("legacy pending decision did not resume after recovery: %v", err)
	}
	resumedRecord, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := store.Decode[model.Task](resumedRecord)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Stage != "review" || resumed.Recoveries != 2 || resumed.Wait != nil {
		t.Fatalf("legacy recovery changed stage or retry history: %+v", resumed)
	}
	savedQuestion, err := e.Store.Get(ctx, "questions", questionID)
	if err != nil {
		t.Fatal(err)
	}
	set, err := store.Decode[model.QuestionSet](savedQuestion)
	if err != nil || set.Status != "answered" || set.Answers["choice"] != "continue reviewed scope" {
		t.Fatalf("legacy decision was not durably preserved: %+v %v", set, err)
	}
}

func TestScopeChangingQuestionReturnsToPlanningAndPreservesWork(t *testing.T) {
	e, task, _ := testEngine(t)
	questionID, questionVersion := seedHumanDecision(t, e, task, "coding", "code", "question")
	if err := e.AnswerQuestionSet(context.Background(), questionID, questionVersion, map[string]string{"choice": "replace database"}, true); err != nil {
		t.Fatal(err)
	}
	record, err := e.Store.Get(context.Background(), "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.Decode[model.Task](record)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Stage != "planning" || updated.PlanVersion != task.PlanVersion+1 || updated.Plan != nil || updated.PlanDocument != nil {
		t.Fatalf("scope change did not invalidate plan and route through planning: %+v", updated)
	}
	if updated.Worktree != "/existing/worktree" || updated.Branch != "existing-branch" || updated.LastAttemptID != "previous" || len(updated.ImplementationAttempts) != 1 || updated.ImplementationAttempts[0] != "previous" {
		t.Fatalf("scope change discarded existing work or attempt history: %+v", updated)
	}
	if _, err := e.Store.Get(context.Background(), "plans", task.ID+":1"); err != nil {
		t.Fatalf("previous immutable plan was removed: %v", err)
	}
}

func TestHumanDecisionRejectsStaleAttemptAndMismatchedResumeRole(t *testing.T) {
	e, task, _ := testEngine(t)
	questionID, questionVersion := seedHumanDecision(t, e, task, "coding", "review", "question")
	err := e.AnswerQuestionSet(context.Background(), questionID, questionVersion, map[string]string{"choice": "answer"}, false)
	if err == nil {
		t.Fatal("review attempt was allowed to resume the coding stage")
	}
	assertPausedDecision(t, e, task.ID)
}

func TestOlderPendingQuestionCannotResumeNewerPauseAndClarificationsKeepRetryBudget(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	oldID, oldVersion := seedHumanDecision(t, e, task, "review", "review", "question")
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	taskRecord, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.Decode[model.Task](taskRecord)
	if err != nil {
		t.Fatal(err)
	}
	run := &agentRun{Engine: e, ctx: ctx, project: project, task: task, input: provider.Input{Role: "review", Purpose: "primary"}}
	choice := model.AgentChoice{Provider: "fake", Model: "fixture"}
	entryID := recoveryEntryID(recoveryEpisodeID(run), choice)
	episode := model.RecoveryEpisode{EpisodeID: recoveryEpisodeID(run), EntryID: entryID, ProjectID: task.ProjectID, TaskID: task.ID, Role: "review", Purpose: "primary", Provider: choice.Provider, Model: choice.Model, PlanVersion: 1, Status: "paused", RestartCount: 2}
	if err := e.Store.Put(ctx, "recovery_episodes", entryID, episode, 0); err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 5; cycle++ {
		id := fmt.Sprintf("current-question-%d", cycle)
		attemptID := fmt.Sprintf("current-attempt-%d", cycle)
		pauseTaskForQuestion(t, e, task.ID, attemptID)
		deadline := time.Now().Add(attemptWallClockLimit).UTC().Format(time.RFC3339Nano)
		attempt := model.Attempt{AttemptID: attemptID, Task: task.ID, ProjectID: task.ProjectID, Role: "review", PlanVersion: 1, Finished: true, Status: "paused", RestartCount: 2, DeadlineAt: deadline}
		if err := e.Store.Put(ctx, "sessions", attemptID, attempt, 0); err != nil {
			t.Fatal(err)
		}
		set := model.QuestionSet{ID: id, TaskID: task.ID, AttemptID: attemptID, Provider: "fake", Questions: []model.Question{{ID: "choice", Text: "Choose?"}}, Status: "pending", PlanVersion: 1}
		if err := e.Store.Put(ctx, "questions", id, set, 0); err != nil {
			t.Fatal(err)
		}
		if cycle == 0 {
			if err := e.AnswerQuestionSet(ctx, oldID, oldVersion, map[string]string{"choice": "stale"}, false); !errors.Is(err, store.ErrConflict) {
				t.Fatalf("older pending question resumed newer pause: %v", err)
			}
		}
		questionRecord, err := e.Store.Get(ctx, "questions", id)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.AnswerQuestionSet(ctx, id, questionRecord.Version, map[string]string{"choice": "answer"}, false); err != nil {
			t.Fatalf("clarification cycle %d failed: %v", cycle+1, err)
		}
		attemptRecord, err := e.Store.Get(ctx, "sessions", attemptID)
		if err != nil {
			t.Fatal(err)
		}
		storedAttempt, err := store.Decode[model.Attempt](attemptRecord)
		if err != nil || storedAttempt.DeadlineAt != deadline {
			t.Fatalf("clarification changed attempt deadline: %+v %v", storedAttempt, err)
		}
		count, _, err := run.persistedRestartState(choice)
		if err != nil || count != 2 {
			t.Fatalf("clarification consumed runtime retry budget: count=%d err=%v", count, err)
		}
	}
	entryRecord, err := e.Store.Get(ctx, "recovery_episodes", entryID)
	if err != nil {
		t.Fatal(err)
	}
	episode, err = store.Decode[model.RecoveryEpisode](entryRecord)
	if err != nil {
		t.Fatal(err)
	}
	episode.Status, episode.FailureKind = "failed", string(provider.FailureTransient)
	if err := e.Store.Put(ctx, "recovery_episodes", entryID, episode, entryRecord.Version); err != nil {
		t.Fatal(err)
	}
	count, _, err := run.persistedRestartState(choice)
	if err != nil || count != 3 {
		t.Fatalf("runtime failure did not retain one configured restart after clarifications: count=%d err=%v", count, err)
	}
}

func pauseTaskForQuestion(t *testing.T, e *Engine, taskID, attemptID string) {
	t.Helper()
	ctx := context.Background()
	record, err := e.Store.Get(ctx, "tasks", taskID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Decode[model.Task](record)
	if err != nil {
		t.Fatal(err)
	}
	task.Stage, task.ResumeStage = "paused", "review"
	task.Wait = &model.TaskWait{AttemptID: attemptID, Kind: "human_decision", ResumeStage: "review", PlanVersion: task.PlanVersion}
	if err := e.Store.Put(ctx, "tasks", taskID, task, record.Version); err != nil {
		t.Fatal(err)
	}
}

func TestPermissionApprovalIsRevalidatedAndConsumedByFreshExactAction(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	first := &agentAttempt{agentRun: &agentRun{Engine: e, ctx: ctx, project: project, task: task, input: provider.Input{Role: "code", CWD: cwd}}, id: "attempt-one", chosen: model.Provider{ID: "fake"}}
	if err := e.Store.Put(ctx, "sessions", first.id, model.Attempt{AttemptID: first.id, Task: task.ID, ProjectID: task.ProjectID, Role: "code", PlanVersion: task.PlanVersion, Finished: true, Status: "paused"}, 0); err != nil {
		t.Fatal(err)
	}
	action := provider.Permission{Tool: "command", Command: []string{"git", "status"}}
	if _, err := first.requestPermission(ctx, action); !errors.Is(err, ErrHumanDecisionPending) {
		t.Fatalf("initial action did not wait for approval: %v", err)
	}
	records, err := e.Store.List(ctx, "permissions")
	if err != nil || len(records) != 1 {
		t.Fatalf("pending permission missing: count=%d err=%v", len(records), err)
	}
	var saved map[string]any
	if err := json.Unmarshal(records[0].Data, &saved); err != nil {
		t.Fatal(err)
	}
	saved["decision"] = "allow"
	if err := e.Store.Put(ctx, "permissions", records[0].ID, saved, records[0].Version); err != nil {
		t.Fatal(err)
	}
	second := &agentAttempt{agentRun: &agentRun{Engine: e, ctx: ctx, project: project, task: task, input: provider.Input{Role: "code", CWD: cwd}}, id: "attempt-two", chosen: model.Provider{ID: "fake"}}
	allowed, err := second.permission(ctx, action)
	if err != nil || !allowed {
		t.Fatalf("fresh exact action did not receive the saved decision: allowed=%v err=%v", allowed, err)
	}
	updated, err := e.Store.Get(ctx, "permissions", records[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var consumed map[string]any
	if err := json.Unmarshal(updated.Data, &consumed); err != nil || consumed["consumed_by_attempt"] != "attempt-two" {
		t.Fatalf("approval was not bound to the new attempt: %+v %v", consumed, err)
	}
	third := &agentAttempt{agentRun: &agentRun{Engine: e, ctx: ctx, project: project, task: task, input: provider.Input{Role: "code", CWD: cwd}}, id: "attempt-three", chosen: model.Provider{ID: "fake"}}
	allowed, err = third.permission(ctx, action)
	if allowed || !errors.Is(err, ErrHumanDecisionPending) {
		t.Fatalf("consumed approval was reused: allowed=%v err=%v", allowed, err)
	}
}

func seedHumanDecision(t *testing.T, e *Engine, task model.Task, resumeStage, role, kind string) (string, int) {
	t.Helper()
	ctx := context.Background()
	record, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.Decode[model.Task](record)
	if err != nil {
		t.Fatal(err)
	}
	task.PlanVersion, task.Stage, task.ResumeStage = 1, "paused", resumeStage
	attemptID := store.ID()
	task.Wait = &model.TaskWait{AttemptID: attemptID, Kind: "human_decision", ResumeStage: resumeStage, PlanVersion: 1}
	task.Plan = &model.Plan{Objective: "existing plan"}
	task.Worktree, task.Branch, task.LastAttemptID = "/existing/worktree", "existing-branch", "previous"
	task.ImplementationAttempts = []string{"previous"}
	if err := e.Store.Put(ctx, "tasks", task.ID, task, record.Version); err != nil {
		t.Fatal(err)
	}
	attempt := model.Attempt{AttemptID: attemptID, Task: task.ID, ProjectID: task.ProjectID, Role: role, PlanVersion: 1, Finished: true, Status: "failed"}
	if err := e.Store.Put(ctx, "sessions", attemptID, attempt, 0); err != nil {
		t.Fatal(err)
	}
	id := store.ID()
	switch kind {
	case "question":
		set := model.QuestionSet{ID: id, TaskID: task.ID, AttemptID: attemptID, Provider: "fixture", Questions: []model.Question{{ID: "choice", Text: "Choose?"}}, Status: "pending", PlanVersion: 1}
		if err := e.Store.Put(ctx, "questions", id, set, 0); err != nil {
			t.Fatal(err)
		}
	case "permission":
		permission := map[string]any{"id": id, "task": task.ID, "attempt_id": attemptID, "provider": "fixture", "request": map[string]any{"tool": "command", "command": []string{"git", "status"}}, "decision": "pending", "plan_version": 1}
		if err := e.Store.Put(ctx, "permissions", id, permission, 0); err != nil {
			t.Fatal(err)
		}
	}
	decisionRecord, err := e.Store.Get(ctx, map[string]string{"question": "questions", "permission": "permissions"}[kind], id)
	if err != nil {
		t.Fatal(err)
	}
	if kind == "question" {
		planRecord, planErr := e.Store.Get(ctx, "plans", task.ID+":1")
		if errors.Is(planErr, store.ErrNotFound) {
			if err := e.Store.Put(ctx, "plans", task.ID+":1", *task.Plan, 0); err != nil {
				t.Fatal(err)
			}
		} else if planErr != nil {
			t.Fatal(planErr)
		} else if _, err := store.Decode[model.Plan](planRecord); err != nil {
			t.Fatal(err)
		}
	}
	return id, decisionRecord.Version
}

func assertResumedDecision(t *testing.T, e *Engine, taskID, stage string, planVersion int) {
	t.Helper()
	record, err := e.Store.Get(context.Background(), "tasks", taskID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Decode[model.Task](record)
	if err != nil {
		t.Fatal(err)
	}
	if task.Stage != stage || task.ResumeStage != "" || task.Wait != nil || task.PlanVersion != planVersion || task.RecoveryGeneration != 0 || task.Recoveries != 0 {
		t.Fatalf("task did not resume exact stage while preserving retry counters: %+v", task)
	}
}

func assertPausedDecision(t *testing.T, e *Engine, taskID string) {
	t.Helper()
	record, err := e.Store.Get(context.Background(), "tasks", taskID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Decode[model.Task](record)
	if err != nil {
		t.Fatal(err)
	}
	if task.Stage != "paused" || task.Wait == nil || task.Wait.Kind != "human_decision" {
		t.Fatalf("mismatched response changed paused task: %+v", task)
	}
}

func storeDecode(record model.Record, out any) error {
	return json.Unmarshal(record.Data, out)
}
