package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"whirlwind/internal/evaluation"
	"whirlwind/internal/model"
)

func TestEvaluationKeepsHistoricalUsageUnknownAndDoesNotStartWork(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "tasks", "old", model.Task{ID: "old", Stage: "complete"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Put(ctx, "sessions", "old-attempt", model.Attempt{Task: "old", AttemptID: "old-attempt", Finished: true}, 0); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Store.List(ctx, "sessions")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/evaluation", nil))
	var report evaluation.Report
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &report) != nil {
		t.Fatalf("evaluation: %d %s", w.Code, w.Body)
	}
	if report.Attempts != 1 || report.UnknownTokenAttempts != 1 || report.VerifiedTasks != 0 {
		t.Fatalf("historical inference misreported: %+v", report)
	}
	after, _ := s.Store.List(ctx, "sessions")
	if len(after) != len(before) {
		t.Fatal("report started inference")
	}
}

func TestTaskOutcomeRecordsFeedbackWithoutClaimingVerification(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "tasks", "task", model.Task{ID: "task", Stage: "pr_created", PR: "pr"}, 0); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://localhost/api/v1/tasks/task/outcome", bytes.NewBufferString(`{"decision":"accepted","reason":"Meets intended use"}`))
	r.Header.Set("If-Match", "1")
	r.Header.Set("Idempotency-Key", "feedback")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("feedback: %d %s", w.Code, w.Body)
	}
	report, err := evaluation.Load(ctx, s.Store)
	if err != nil {
		t.Fatal(err)
	}
	if report.UserAcceptedTasks != 1 || report.VerifiedTasks != 0 {
		t.Fatalf("acceptance and verification conflated: %+v", report)
	}
	r = httptest.NewRequest("POST", "http://localhost/api/v1/tasks/task/outcome", bytes.NewBufferString(`{"decision":"regression"}`))
	r.Header.Set("If-Match", "1")
	r.Header.Set("Idempotency-Key", "stale-feedback")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatal("stale feedback overwrote current task version")
	}
}
