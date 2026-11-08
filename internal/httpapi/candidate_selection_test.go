package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	candidatePolicyHash = "approved-policy"
	candidateTitle      = "Fix retries"
)

func candidateAction(t *testing.T, s *Server, id, action, version, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest("POST", "http://localhost/api/v1/directions/"+id+"/"+action, strings.NewReader(body))
	request.Header.Set("If-Match", version)
	request.Header.Set(idempotencyKeyHeader, store.ID())
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	return response
}

func TestSelectionPersistsAuthorizationBeforePlanning(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	_ = s.Store.Put(ctx, "projects", "p", model.Project{ID: "p", PolicyHash: candidatePolicyHash}, 0)
	_ = s.Store.Put(ctx, "directions", "idea", model.Direction{ID: "idea", ProjectID: "p", Title: candidateTitle, Description: "Preserve pending work"}, 0)
	response := candidateAction(t, s, "idea", "select", "1", `{}`)
	if response.Code != 202 {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
	record, err := s.Store.Get(ctx, "tasks", "direction-idea")
	if err != nil {
		t.Fatal(err)
	}
	var task map[string]any
	_ = json.Unmarshal(record.Data, &task)
	authorization, ok := task["selection"].(map[string]any)
	if !ok || authorization["candidate_id"] != "idea" || authorization["policy_hash"] != candidatePolicyHash || authorization["objective"] != candidateTitle {
		t.Fatalf("selection authorization missing: %s", record.Data)
	}
}

func TestDismissedCandidateCannotStartUntilReopened(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	_ = s.Store.Put(ctx, "projects", "p", model.Project{ID: "p", PolicyHash: candidatePolicyHash}, 0)
	_ = s.Store.Put(ctx, "directions", "idea", model.Direction{ID: "idea", ProjectID: "p", Title: candidateTitle, Description: "Preserve pending work"}, 0)
	dismissed := candidateAction(t, s, "idea", "dismiss", "1", `{}`)
	if dismissed.Code != 200 {
		t.Fatalf("dismiss: %d %s", dismissed.Code, dismissed.Body)
	}
	if selected := candidateAction(t, s, "idea", "select", "2", `{}`); selected.Code != 409 {
		t.Fatalf("dismissed candidate started: %d %s", selected.Code, selected.Body)
	}
	if reopened := candidateAction(t, s, "idea", "reopen", "2", `{}`); reopened.Code != 200 {
		t.Fatalf("reopen: %d %s", reopened.Code, reopened.Body)
	}
	if selected := candidateAction(t, s, "idea", "select", "3", `{}`); selected.Code != 202 {
		t.Fatalf("reopened selection: %d %s", selected.Code, selected.Body)
	}
}

func TestIdeationBriefPersistsWithoutAuthorizingPlanning(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	s.Context = cancelled
	_ = s.Store.Put(ctx, "projects", "p", model.Project{ID: "p"}, 0)
	req := httptest.NewRequest("POST", "http://localhost/api/v1/projects/p/ideation", strings.NewReader(`{"goal":"Reduce missed work","constraints":"Keep data on this machine","priorities":"Recovery before expansion","max_candidates":2}`))
	req.Header.Set(idempotencyKeyHeader, "brief")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, req)
	if response.Code != 202 {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
	record, err := s.Store.Get(ctx, "ideation_briefs", "p")
	if err != nil {
		t.Fatal(err)
	}
	var brief map[string]any
	_ = json.Unmarshal(record.Data, &brief)
	if brief["goal"] != "Reduce missed work" || brief["constraints"] != "Keep data on this machine" || brief["priorities"] != "Recovery before expansion" || brief["max_candidates"] != float64(2) {
		t.Fatalf("brief missing: %s", record.Data)
	}
	tasks, _ := s.Store.List(ctx, "tasks")
	if len(tasks) != 0 {
		t.Fatal("brief authorized planning")
	}
}

func TestGlobalRunnerToggleDoesNotAdoptUnselectedIntake(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "tasks", "intake", model.Task{ID: "intake", Stage: "awaiting_selection"}, 0); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"stop", "resume"} {
		request := httptest.NewRequest("POST", "http://localhost/api/v1/runners/"+action, strings.NewReader(`{}`))
		request.Header.Set(idempotencyKeyHeader, action)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", action, response.Code, response.Body)
		}
	}
	record, _ := s.Store.Get(ctx, "tasks", "intake")
	task, _ := store.Decode[model.Task](record)
	if task.Stage != "awaiting_selection" || task.HasSelection() {
		t.Fatalf("runner toggle changed selection: %+v", task)
	}
}
