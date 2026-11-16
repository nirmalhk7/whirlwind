package store

import (
	"context"
	"testing"

	"whirlwind/internal/model"
)

func TestRunLogDetailsIgnoreMissingQuestionRun(t *testing.T) {
	s := openTest(t)
	question := model.QuestionSet{AttemptID: "missing", Status: "pending", Questions: []model.Question{{Text: "Existing orphan"}}}
	if err := s.Put(context.Background(), "questions", "orphan", question, 0); err != nil {
		t.Fatalf("logging rejected existing question storage contract: %v", err)
	}
}

func TestRunLogDetailsIgnoreUnrelatedQuestionShape(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	unrelated := map[string]any{"attempt_id": "other", "plan_id": "other-task", "status": 7}
	if err := s.Put(ctx, "questions", "other", unrelated, 0); err != nil {
		t.Fatalf("logging validated unrelated question fields: %v", err)
	}
	if err := s.Put(ctx, "sessions", "current", model.Attempt{Role: "code", Status: "running"}, 0); err != nil {
		t.Fatalf("unrelated question broke run logging: %v", err)
	}
}
