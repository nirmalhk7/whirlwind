package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

func TestRunLogPersistsRunDetailsWithoutInventingEvidence(t *testing.T) {
	s, _, path := persistedLogFixture(t)
	ctx := context.Background()
	attempt := model.Attempt{Provider: "cursor", Role: "plan", ResolvedModel: "chosen-model", Status: "failed", Error: "connection lost"}
	if err := s.Put(ctx, "sessions", persistedRunID, attempt, 1); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"stype=run/details", "cursor", "chosen-model", "connection lost", "failed"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("persisted details missing %q: %s", want, body)
		}
	}
	events, err := s.AttemptEvents(ctx, persistedRunID, 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatalf("context logging changed structured evidence: %v %v", events, err)
	}
	before := string(body)
	attempt.WorkerHeartbeatAt = "2026-11-10T12:00:00Z"
	if err := s.Put(ctx, "sessions", persistedRunID, attempt, 2); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != before {
		t.Fatalf("heartbeat duplicated unchanged details: %q %v", after, err)
	}
}

func TestRunLogPersistsPlanAndPendingQuestionChanges(t *testing.T) {
	s, _, path := persistedLogFixture(t)
	ctx := context.Background()
	if err := s.Put(ctx, "sessions", persistedRunID, model.Attempt{Task: "task", Status: "paused"}, 1); err != nil {
		t.Fatal(err)
	}
	task := model.ExecutionPlan{ID: "task", Stage: "plan_approval", Branch: "delivery/saved", Plan: &model.PlanSpecification{Objective: "Preserve operator choices"}}
	if err := s.Put(ctx, "execution_plans", task.ID, task, 0); err != nil {
		t.Fatal(err)
	}
	question := model.QuestionSet{TaskID: task.ID, AttemptID: persistedRunID, Status: "pending", Questions: []model.Question{{Text: "Which directory should receive output?"}}}
	if err := s.Put(ctx, "questions", "question", question, 0); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Preserve operator choices", "plan_approval", "delivery/saved", "Which directory"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("updated details missing %q: %s", want, body)
		}
	}
	question.Status = "answered"
	if err := s.Put(ctx, "questions", "question", question, 1); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if err != nil || strings.Contains(lines[len(lines)-1], "Which directory") {
		t.Fatalf("resolved question remains pending in latest snapshot: %s %v", body, err)
	}
}
