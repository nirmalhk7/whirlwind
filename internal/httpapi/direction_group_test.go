package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	directionGroupWorktree  = "/saved-work"
	firstDirectionTaskID    = "direction-a0"
	firstDirectionGroupPath = "directions/a0/group"
)

func setupDirectionGroups(t *testing.T) *Server {
	t.Helper()
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "projects", "p", model.Project{ID: "p", PolicyHash: "confirmed"}, 0); err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{"a", "b"} {
		for i, category := range []string{"expand", "optimize", "harden", "simplify"} {
			id := fmt.Sprintf("%s%d", group, i)
			value := map[string]any{"id": id, "project_id": "p", "day": "2026-10-19", "group_id": group, "category": category, "selected": i == 0}
			if err := s.Store.Put(ctx, "directions", id, value, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.Store.Put(ctx, "tasks", firstDirectionTaskID, model.Task{ID: firstDirectionTaskID, ProjectID: "p", Stage: "coding", Worktree: directionGroupWorktree}, 0); err != nil {
		t.Fatal(err)
	}
	return s
}

func directionRequest(s *Server, method, path, version, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost/api/v1/"+path, strings.NewReader(body))
	r.Header.Set("If-Match", version)
	r.Header.Set(idempotencyKeyHeader, key)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestSelectingDirectionReplacesOnlyItsGroupChoice(t *testing.T) {
	s := setupDirectionGroups(t)
	ctx := context.Background()
	w := directionRequest(s, "POST", "directions/a1/select", "1", "switch", "{}")
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for id, want := range map[string]bool{"a0": false, "a1": true, "a2": false, "a3": false, "b0": true} {
		record, err := s.Store.Get(ctx, "directions", id)
		if err != nil {
			t.Fatal(err)
		}
		d, err := store.Decode[model.Direction](record)
		if err != nil || d.Selected != want {
			t.Fatalf("%s: %+v %v", id, d, err)
		}
	}
	record, _ := s.Store.Get(ctx, "tasks", firstDirectionTaskID)
	task, _ := store.Decode[model.Task](record)
	if task.Stage != "paused" || task.ResumeStage != "coding" || task.Worktree != directionGroupWorktree {
		t.Fatalf("previous task lost: %+v", task)
	}
	stale := directionRequest(s, "POST", "directions/a2/select", "1", "stale-peer", "{}")
	if stale.Code != 409 {
		t.Fatalf("stale group snapshot accepted: %d %s", stale.Code, stale.Body)
	}
}

func TestDeleteDirectionGroupIsAtomicVersionedAndReplayable(t *testing.T) {
	s := setupDirectionGroups(t)
	assertStaleDirectionGroupDelete(t, s)
	deleteAndReplayDirectionGroup(t, s)
	assertDirectionGroupTaskPaused(t, s)
	assertDirectionGroupDeleteEvents(t, s)
}

func assertStaleDirectionGroupDelete(t *testing.T, s *Server) {
	t.Helper()
	stale := directionRequest(s, "DELETE", firstDirectionGroupPath, "1", "stale-delete", `{"versions":{"a0":1,"a1":2,"a2":1,"a3":1}}`)
	if stale.Code != 409 {
		t.Fatalf("stale delete: %d %s", stale.Code, stale.Body)
	}
	if records, _ := s.Store.List(context.Background(), "directions"); len(records) != 8 {
		t.Fatal("stale deletion changed group")
	}
}

func deleteAndReplayDirectionGroup(t *testing.T, s *Server) {
	t.Helper()
	body := `{"versions":{"a0":1,"a1":1,"a2":1,"a3":1}}`
	response := directionRequest(s, "DELETE", firstDirectionGroupPath, "1", "delete-group", body)
	if response.Code != 200 {
		t.Fatalf("delete: %d %s", response.Code, response.Body)
	}
	records, _ := s.Store.List(context.Background(), "directions")
	if len(records) != 4 {
		t.Fatalf("remaining ideas: %d", len(records))
	}
	for _, record := range records {
		if !strings.HasPrefix(record.ID, "b") {
			t.Fatal("wrong group deleted")
		}
	}
	replay := directionRequest(s, "DELETE", firstDirectionGroupPath, "1", "delete-group", body)
	if replay.Code != 200 || replay.Body.String() != response.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
}

func assertDirectionGroupTaskPaused(t *testing.T, s *Server) {
	t.Helper()
	record, _ := s.Store.Get(context.Background(), "tasks", firstDirectionTaskID)
	task, _ := store.Decode[model.Task](record)
	if task.Stage != "paused" || task.ResumeStage != "coding" || task.Worktree != directionGroupWorktree {
		t.Fatalf("work lost: %+v", task)
	}
}

func assertDirectionGroupDeleteEvents(t *testing.T, s *Server) {
	t.Helper()
	events, _ := s.Store.Events(context.Background(), 0, 200)
	deleted := 0
	for _, event := range events {
		if event.Kind == "directions" && strings.Contains(string(event.Data), `"deleted":true`) {
			deleted++
		}
	}
	if deleted != 4 {
		t.Fatalf("deletion events: %d", deleted)
	}
}

func TestSwitchingOrDeletingGloballyStoppedChoiceKeepsItStoppedOnResume(t *testing.T) {
	for _, action := range []string{"switch", "delete"} {
		t.Run(action, func(t *testing.T) { assertGlobalStopStaysStopped(t, action) })
	}
}

func assertGlobalStopStaysStopped(t *testing.T, action string) {
	s := setupDirectionGroups(t)
	ctx := context.Background()
	record, _ := s.Store.Get(ctx, "tasks", firstDirectionTaskID)
	task, _ := store.Decode[model.Task](record)
	task.Stage = "paused"
	task.ResumeStage = "coding"
	task.Error = globalStopReason
	if err := s.Store.Put(ctx, "tasks", record.ID, task, record.Version); err != nil {
		t.Fatal(err)
	}
	response := globallyStoppedDirectionAction(s, action)
	if response.Code >= 300 {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
	record, _ = s.Store.Get(ctx, "tasks", firstDirectionTaskID)
	task, _ = store.Decode[model.Task](record)
	if task.Stage != "paused" || task.ResumeStage != "coding" || task.Error == globalStopReason {
		t.Fatalf("global resume could revive old choice: %+v", task)
	}
}

func globallyStoppedDirectionAction(s *Server, action string) *httptest.ResponseRecorder {
	if action == "switch" {
		return directionRequest(s, "POST", "directions/a1/select", "1", "switch-paused", "{}")
	}
	return directionRequest(s, "DELETE", firstDirectionGroupPath, "1", "delete-paused", `{"versions":{"a0":1,"a1":1,"a2":1,"a3":1}}`)
}
