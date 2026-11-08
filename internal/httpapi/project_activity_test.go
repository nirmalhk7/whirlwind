package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func projectActivityRequest(s *Server, action, version, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://localhost/api/v1/projects/p/"+action, strings.NewReader(`{}`))
	r.Header.Set("If-Match", version)
	r.Header.Set(idempotencyKeyHeader, key)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestProjectPauseResumeIsVersionedAndIndependent(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	for _, id := range []string{"p", "other"} {
		if err := s.Store.Put(ctx, "projects", id, model.Project{ID: id}, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		version, key string
		status       int
	}{{"", "missing", 428}, {"9", "stale", 409}, {"1", "pause", 200}} {
		if w := projectActivityRequest(s, "pause", tc.version, tc.key); w.Code != tc.status {
			t.Fatalf("pause: %d %s", w.Code, w.Body)
		}
	}
	assertProjectPaused(t, s, "p", true)
	assertProjectPaused(t, s, "other", false)
	w := projectActivityRequest(s, "resume", "2", "resume")
	if w.Code != 200 {
		t.Fatalf("resume: %d %s", w.Code, w.Body)
	}
	assertProjectPaused(t, s, "p", false)
	// A retried old pause must not undo a newer resume.
	if w := projectActivityRequest(s, "pause", "1", "pause"); w.Code != 200 {
		t.Fatal(w.Body)
	}
	assertProjectPaused(t, s, "p", false)
	if _, err := s.Store.Get(ctx, "settings", "global"); err != store.ErrNotFound {
		t.Fatalf("project action changed global settings: %v", err)
	}
}

func assertProjectPaused(t *testing.T, s *Server, id string, want bool) {
	t.Helper()
	r, err := s.Store.Get(context.Background(), "projects", id)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Decode[map[string]any](r)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := p["paused"].(bool)
	if got != want {
		t.Fatalf("%s paused=%v want=%v", id, got, want)
	}
}

func TestProjectPauseReceiptFailureRollsBack(t *testing.T) {
	s := setup(t)
	if err := s.Store.Put(context.Background(), "projects", "p", model.Project{ID: "p"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec("CREATE TRIGGER fail_pause_receipt BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	w := projectActivityRequest(s, "pause", "1", "failed")
	if w.Code != 503 {
		t.Fatalf("pause: %d %s", w.Code, w.Body)
	}
	assertProjectPaused(t, s, "p", false)
}

func TestPausedProjectRejectsManualIdeationWithoutQueuingOperation(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "projects", "p", model.Project{ID: "p", Paused: true}, 0); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://localhost/api/v1/projects/p/ideation", strings.NewReader(`{}`))
	r.Header.Set(idempotencyKeyHeader, "paused-ideation")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"project_paused"`) {
		t.Fatalf("ideation: %d %s", w.Code, w.Body)
	}
	operations, err := s.Store.List(ctx, "operations")
	if err != nil || len(operations) != 0 {
		t.Fatalf("paused request queued operation: %+v %v", operations, err)
	}
}

func TestProjectPauseAndGlobalStopRemainIndependent(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "projects", "p", model.Project{ID: "p"}, 0); err != nil {
		t.Fatal(err)
	}
	for _, task := range []model.Task{{ID: "queued", ProjectID: "p", Stage: "planning"}, {ID: "manual", ProjectID: "p", Stage: "paused", ResumeStage: "review", Error: "Paused by user"}} {
		task.Selection = &model.SelectionAuthorization{CandidateID: task.ID, SelectedAt: "2026-10-22T00:00:00Z"}
		if err := s.Store.Put(ctx, "tasks", task.ID, task, 0); err != nil {
			t.Fatal(err)
		}
	}
	if w := projectActivityRequest(s, "pause", "1", "project-pause"); w.Code != 200 {
		t.Fatal(w.Body)
	}
	for _, id := range []string{"queued", "manual"} {
		r, err := s.Store.Get(ctx, "tasks", id)
		if err != nil || r.Version != 1 {
			t.Fatalf("project pause changed saved task: %+v %v", r, err)
		}
	}
	for _, action := range []string{"stop", "resume"} {
		r := httptest.NewRequest("POST", "http://localhost/api/v1/runners/"+action, strings.NewReader(`{}`))
		r.Header.Set(idempotencyKeyHeader, "global-"+action)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("global %s: %d %s", action, w.Code, w.Body)
		}
		assertProjectPaused(t, s, "p", true)
	}
	r, _ := s.Store.Get(ctx, "tasks", "manual")
	task, _ := store.Decode[model.Task](r)
	if task.Stage != "paused" || task.Error != "Paused by user" {
		t.Fatalf("manual pause lost: %+v", task)
	}
}
