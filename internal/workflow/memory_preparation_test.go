package workflow

import (
	"context"
	"slices"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

const automaticRecallFixtureTrigger = "boilerplate-only"

func memoryPreparationRun(t *testing.T) *agentRun {
	t.Helper()
	engine, task, cwd := testEngine(t)
	project, err := engine.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	task.Issue.Title = "task objective"
	task.Issue.Body = ""
	lesson := model.Lesson{ID: "static-only", ProjectID: project.ID, Role: "code", Status: "tentative", Trigger: automaticRecallFixtureTrigger, Mistake: "old assumption", Correction: "Unrelated lesson sentinel", EvidenceIDs: []string{"history"}}
	if err := engine.Store.Put(context.Background(), "lessons", lesson.ID, lesson, 0); err != nil {
		t.Fatal(err)
	}
	return &agentRun{Engine: engine, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: "code", CWD: cwd, Prompt: automaticRecallFixtureTrigger}}
}

func TestAutomaticRecallExcludesStaticPrompt(t *testing.T) {
	run := memoryPreparationRun(t)
	run.prepareLearning()
	if len(run.learning.Recalled) != 0 {
		t.Fatal("static prompt selected unrelated lesson")
	}
}

func TestAutomaticRecallIncludesHandoffFacts(t *testing.T) {
	run := memoryPreparationRun(t)
	run.input.Prompt = "staticinstructions"
	run.task.Handoff.FailingTests = []string{automaticRecallFixtureTrigger}
	run.prepareLearning()
	if len(run.learning.Recalled) != 1 {
		t.Fatal("handoff failure omitted from memory query")
	}
}

func TestLearningProtocolSurvivesBudgetReadFailure(t *testing.T) {
	run := memoryPreparationRun(t)
	if err := run.Store.Close(); err != nil {
		t.Fatal(err)
	}
	run.prepareLearning()
	if !strings.Contains(run.input.Prompt, "Learning protocol:") {
		t.Fatal("budget read failure dropped learning protocol")
	}
	if !slices.Contains(run.learning.Warnings, "Learning budget unavailable") {
		t.Fatal("budget failure warning absent")
	}
}

func TestAutomaticRecallReportsBudgetWriteFailure(t *testing.T) {
	run := memoryPreparationRun(t)
	run.task.Issue.Title = automaticRecallFixtureTrigger
	_, err := run.Store.DB.Exec(`CREATE TRIGGER reject_memory_budget BEFORE INSERT ON resources WHEN NEW.kind='memory_budgets' BEGIN SELECT RAISE(FAIL,'budget persistence failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	run.prepareLearning()
	if !slices.Contains(run.learning.Warnings, "Learning budget update unavailable") {
		t.Fatal("automatic budget write failure silently ignored")
	}
	if len(run.learning.Recalled) != 0 {
		t.Fatal("unrecorded automatic advice delivered")
	}
}
