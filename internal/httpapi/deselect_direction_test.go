package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	directionTaskID   = "direction-idea"
	directionWorktree = "/saved-work"
)

func TestDeselectDirectionPausesTaskAndReselectRestoresIt(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "projects", "p", model.Project{ID: "p", PolicyHash: "confirmed"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Put(ctx, "directions", "idea", model.Direction{ID: "idea", ProjectID: "p", Selected: true}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Put(ctx, "tasks", directionTaskID, model.Task{ID: directionTaskID, ProjectID: "p", Stage: "coding", Worktree: directionWorktree}, 0); err != nil {
		t.Fatal(err)
	}
	send := func(action, version, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://localhost/api/v1/directions/idea/"+action, strings.NewReader(`{}`))
		req.Header.Set("If-Match", version)
		req.Header.Set(idempotencyKeyHeader, key)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		return response
	}
	response := send("deselect", "1", "deselect")
	if response.Code != 200 {
		t.Fatalf("deselect: %d %s", response.Code, response.Body)
	}
	d, _ := s.Store.Get(ctx, "directions", "idea")
	direction, _ := store.Decode[model.Direction](d)
	taskRecord, _ := s.Store.Get(ctx, "tasks", directionTaskID)
	task, _ := store.Decode[model.Task](taskRecord)
	if direction.Selected || task.Stage != "paused" || task.ResumeStage != "coding" || task.Worktree != directionWorktree {
		t.Fatalf("unsafe deselection: %+v %+v", direction, task)
	}
	if replay := send("deselect", "1", "deselect"); replay.Code != 200 || replay.Body.String() != response.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	if stale := send("select", "1", "stale"); stale.Code != 409 {
		t.Fatalf("stale: %d %s", stale.Code, stale.Body)
	}
	if selected := send("select", "2", "reselect"); selected.Code != 202 {
		t.Fatalf("reselect: %d %s", selected.Code, selected.Body)
	}
	taskRecord, _ = s.Store.Get(ctx, "tasks", directionTaskID)
	task, _ = store.Decode[model.Task](taskRecord)
	if task.Stage != "coding" || task.Error != "" || task.Worktree != directionWorktree {
		t.Fatalf("did not restore task: %+v", task)
	}
	tasks, _ := s.Store.List(ctx, "tasks")
	if len(tasks) != 1 {
		t.Fatal("reselection duplicated task")
	}
}

func TestDeselectDirectionPreservesPausedAndPublishedWork(t *testing.T) {
	for _, stage := range []string{"paused", "complete", "pr_created", "missing"} {
		t.Run(stage, func(t *testing.T) {
			assertDeselectPreservesStage(t, stage)
		})
	}
}

func assertDeselectPreservesStage(t *testing.T, stage string) {
	t.Helper()
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "directions", "idea", model.Direction{ID: "idea", Selected: true}, 0); err != nil {
		t.Fatal(err)
	}
	if stage != "missing" {
		if err := s.Store.Put(ctx, "tasks", directionTaskID, model.Task{ID: directionTaskID, Stage: stage, Error: "Existing state", PR: "https://example.test/pr/1"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest("POST", "http://localhost/api/v1/directions/idea/deselect", strings.NewReader(`{}`))
	req.Header.Set("If-Match", "1")
	req.Header.Set(idempotencyKeyHeader, "deselect")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
	assertDirectionTaskPreserved(t, s.Store, ctx, stage)
}

func assertDirectionTaskPreserved(t *testing.T, db *store.Store, ctx context.Context, stage string) {
	t.Helper()
	if stage == "missing" {
		return
	}
	record, _ := db.Get(ctx, "tasks", directionTaskID)
	if record.Version != 1 {
		t.Fatal("existing task state changed")
	}
}
