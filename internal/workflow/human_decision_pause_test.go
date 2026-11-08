package workflow

import (
	"context"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestHumanDecisionEndsAttemptAndPersistsPausedTask(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	record, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	stage := task.Stage
	attemptID := "paused-human-decision-attempt"
	if err := e.Store.Put(ctx, "sessions", attemptID, model.Attempt{
		AttemptID:   attemptID,
		Task:        task.ID,
		ProjectID:   task.ProjectID,
		Role:        stageRole(stage),
		PlanVersion: task.PlanVersion,
		Finished:    true,
		Status:      "paused",
	}, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "questions", "pending-human-decision", model.QuestionSet{
		ID:          "pending-human-decision",
		TaskID:      task.ID,
		AttemptID:   attemptID,
		Questions:   []model.Question{{ID: "choice", Text: "Choose?"}},
		Status:      "pending",
		PlanVersion: task.PlanVersion,
	}, 0); err != nil {
		t.Fatal(err)
	}
	step := &taskStep{Engine: e, ctx: ctx, project: project, task: task, record: record}
	if err = step.pauseForHumanDecision(); err != nil {
		t.Fatal(err)
	}
	record, err = e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := store.Decode[model.Task](record)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Stage != "paused" || paused.ResumeStage != stage || paused.Wait == nil || paused.Wait.Kind != "human_decision" || paused.Wait.AttemptID != attemptID {
		t.Fatalf("human decision did not pause the current stage: %+v", paused)
	}
}

func stageRole(stage string) string {
	switch stage {
	case "planning", "questions":
		return "plan"
	case "review":
		return "review"
	case "submitting", "publishing":
		return "finalizer"
	default:
		return "code"
	}
}
