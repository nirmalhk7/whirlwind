package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func deleteProjectRequest(s *Server, version, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("DELETE", "http://localhost/api/v1/projects/p", nil)
	r.Header.Set("If-Match", version)
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestDeleteProjectPreservesRepositoryAndHistory(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	path := seedDeleteProjectHistory(t, s, ctx)
	for _, tc := range []struct {
		version, key string
		status       int
	}{{"", "missing", 428}, {"9", "stale", 409}} {
		if w := deleteProjectRequest(s, tc.version, tc.key); w.Code != tc.status {
			t.Fatalf("status=%d body=%s", w.Code, w.Body)
		}
	}
	first := deleteProjectRequest(s, "1", "delete")
	if first.Code != 200 {
		t.Fatalf("delete: %d %s", first.Code, first.Body)
	}
	replay := deleteProjectRequest(s, "1", "delete")
	if replay.Code != 200 || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	if _, err := s.Store.Get(ctx, "projects", "p"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("project remains: %v", err)
	}
	assertDeletedProjectHistory(t, s, ctx, path)
	if w := deleteProjectRequest(s, "1", "again"); w.Code != 404 {
		t.Fatalf("missing project: %d", w.Code)
	}
}

func TestDeleteProjectBlocksActiveWork(t *testing.T) {
	for _, kind := range []string{"task", "generation", "daily", "task-lease", "queued-ideation"} {
		t.Run(kind, func(t *testing.T) {
			s := setup(t)
			ctx := context.Background()
			if err := s.Store.Put(ctx, "projects", "p", model.Project{ID: "p"}, 0); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "task":
				_ = s.Store.Put(ctx, "tasks", "t", model.Task{ID: "t", ProjectID: "p", Stage: "coding"}, 0)
			case "task-lease":
				_ = s.Store.Put(ctx, "tasks", "t", model.Task{ID: "t", ProjectID: "p", Stage: "paused"}, 0)
				_, _ = s.Store.Claim(ctx, "task:t", "worker", time.Minute)
			case "generation":
				_, _ = s.Store.Claim(ctx, "generation:p", "worker", time.Minute)
			case "daily":
				_, _ = s.Store.Claim(ctx, "ideation:p:2026-10-14", "worker", time.Minute)
			case "queued-ideation":
				_ = s.Store.Put(ctx, "operations", "o", model.Operation{Kind: "ideation", Subject: "p", Stage: "running"}, 0)
			}
			w := deleteProjectRequest(s, "1", "blocked")
			if w.Code != 409 {
				t.Fatalf("active deletion: %d %s", w.Code, w.Body)
			}
			if _, err := s.Store.Get(ctx, "projects", "p"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func seedDeleteProjectHistory(t *testing.T, s *Server, ctx context.Context) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(path, []byte("user work"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []struct {
		kind, id string
		data     any
	}{
		{"projects", "p", model.Project{ID: "p", Name: "Project", Path: filepath.Dir(path)}},
		{"tasks", "t", model.Task{ID: "t", ProjectID: "p", Stage: "paused"}},
		{"sessions", "run", map[string]any{"project_id": "p", "finished": true}},
		{"projects", "other", model.Project{ID: "other"}},
	} {
		if err := s.Store.Put(ctx, value.kind, value.id, value.data, 0); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func assertDeletedProjectHistory(t *testing.T, s *Server, ctx context.Context, path string) {
	t.Helper()
	for _, ref := range [][2]string{{"tasks", "t"}, {"sessions", "run"}, {"projects", "other"}} {
		if _, err := s.Store.Get(ctx, ref[0], ref[1]); err != nil {
			t.Fatal(err)
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "user work" {
		t.Fatalf("source changed: %s %v", data, err)
	}
	events, err := s.Store.Events(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Kind == "projects" && event.ResourceID == "p" && string(event.Data) == `{"deleted":true}` {
			found = true
		}
	}
	if !found {
		t.Fatal("missing deletion event")
	}
}
