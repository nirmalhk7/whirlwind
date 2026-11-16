package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

func TestRunLogDetailsScopeAndRedactPendingQuestions(t *testing.T) {
	s, _, path := persistedLogFixture(t)
	ctx := context.Background()
	if err := s.Put(ctx, "sessions", persistedRunID, model.Attempt{Task: "task", Status: "paused"}, 1); err != nil {
		t.Fatal(err)
	}
	question := model.QuestionSet{AttemptID: persistedRunID, TaskID: "task", Status: "pending", Answers: map[string]string{"key": "private-answer"}, Questions: []model.Question{{Secret: true, Text: "private-prompt", Answer: "private-value"}}}
	if err := s.Put(ctx, "questions", "secret", question, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "sessions", "other-run", model.Attempt{Task: "other-task", Status: "running"}, 0); err != nil {
		t.Fatal(err)
	}
	other := model.QuestionSet{AttemptID: "other-run", TaskID: "task", Status: "pending", Questions: []model.Question{{Text: "Other run question"}}}
	if err := s.Put(ctx, "questions", "other", other, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "execution_plans", "task", model.ExecutionPlan{ID: "task", Stage: "questions"}, 0); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-answer", "private-value", "private-prompt", "Other run question"} {
		if strings.Contains(string(body), secret) {
			t.Errorf("run details disclosed %q", secret)
		}
	}
	if !strings.Contains(string(body), "[redacted]") {
		t.Fatal("secret question was not redacted")
	}
}
