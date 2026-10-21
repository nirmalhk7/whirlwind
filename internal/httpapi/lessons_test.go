package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"whirlwind/internal/model"
)

func setupLessons(t *testing.T) *Server {
	t.Helper()
	s := setup(t)
	ctx := context.Background()
	for _, id := range []string{"p", "other"} {
		if err := s.Store.Put(ctx, "projects", id, model.Project{ID: id}, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ id, project, role string }{{"one", "p", "code"}, {"two", "p", "plan"}, {"three", "other", "code"}} {
		if err := s.Store.Put(ctx, "lessons", item.id, map[string]any{"project_id": item.project, "role": item.role, "status": "tentative", "trigger": "schema", "correction": "update schema"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestLessonsListFiltersAndRetireReplays(t *testing.T) {
	s := setupLessons(t)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/"+path, nil))
		return w
	}
	w := get("lessons?project_id=p&role=code&limit=1")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"one"`) || strings.Contains(w.Body.String(), `"id":"three"`) {
		t.Fatalf("scoped page: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"lessons", "lessons?project_id=p&role=bogus", "lessons?project_id=p&limit=501", "lessons?project_id=p&after=-1"} {
		if got := get(path); got.Code != 422 {
			t.Fatalf("%s: %d %s", path, got.Code, got.Body)
		}
	}
	retire := func(version, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/api/v1/lessons/one/retire", strings.NewReader(`{}`))
		r.Header.Set(ifMatchHeader, version)
		r.Header.Set(idempotencyKeyHeader, key)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	a, b := retire("1", "retire"), retire("1", "retire")
	if a.Code != 200 || b.Code != 200 || a.Body.String() != b.Body.String() {
		t.Fatalf("retirement replay: %d %s / %d %s", a.Code, a.Body, b.Code, b.Body)
	}
	if got := retire("1", "stale"); got.Code != 409 {
		t.Fatalf("stale write: %d %s", got.Code, got.Body)
	}
	var record model.Record
	_ = json.Unmarshal(get("lessons/one").Body.Bytes(), &record)
	if !strings.Contains(string(record.Data), `"status":"retired"`) {
		t.Fatalf("not retired: %s", record.Data)
	}
	if got := retire("2", "retire-again"); got.Code != 200 {
		t.Fatalf("repeat retirement: %d %s", got.Code, got.Body)
	}
}
