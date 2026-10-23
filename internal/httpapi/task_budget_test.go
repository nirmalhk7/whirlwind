package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"whirlwind/internal/model"
)

// A stale writer must not overwrite the allowance another operator already changed.
func TestTaskBudgetMutationRequiresCurrentVersion(t *testing.T) {
	s := setup(t)
	if err := s.Store.Put(context.Background(), "tasks", "task", model.Task{ID: "task", Stage: "paused"}, 0); err != nil {
		t.Fatal(err)
	}
	request := func(version, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", "http://localhost/api/v1/tasks/task/budget", bytes.NewBufferString(`{"max_attempts":3,"max_runtime_seconds":60}`))
		r.Header.Set("If-Match", version)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("1", "set"); w.Code != 200 {
		t.Fatalf("set budget: %d %s", w.Code, w.Body)
	}
	if w := request("1", "stale"); w.Code != 409 {
		t.Fatalf("stale budget accepted: %d %s", w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", "http://localhost/api/v1/tasks/task/budget", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var out struct {
		Budget model.BudgetPolicy `json:"budget"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Budget.MaxAttempts != 3 {
		t.Fatalf("missing configured allowance: %s", w.Body)
	}
}

// Null usage after confirmed termination releases only concurrency; unknown spend survives the API mutation.
func TestBudgetReconciliationRetainsUnknownConsumption(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "tasks", "t", model.Task{ID: "t", Stage: "paused"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.ReserveBudget(ctx, model.BudgetReservation{AttemptID: "a", TaskID: "t", Owner: "old", Pool: "pool"}, model.BudgetPolicy{MaxAttempts: 3, MaxRuntimeSeconds: 60, AttemptTokens: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.RecoverInterrupted(ctx, "new"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://localhost/api/v1/tasks/t/budget-reconcile", bytes.NewBufferString(`{"attempt_id":"a","confirmed_terminated":true,"usage":null}`))
	r.Header.Set("If-Match", "1")
	r.Header.Set("Idempotency-Key", "reconcile")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("reconcile: %d %s", w.Code, w.Body)
	}
	report, err := s.Store.TaskUsage(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	if report.ReservedTokens != 100 || report.UncertainAttempts != 1 || len(report.UncertainReservations) != 1 || report.UncertainReservations[0].Status != "uncertain_released" {
		t.Fatalf("API refunded unknown usage: %+v", report)
	}
}
