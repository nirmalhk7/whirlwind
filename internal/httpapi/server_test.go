package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"khan/internal/github"
	"khan/internal/model"
	"khan/internal/store"
	"khan/internal/workflow"
)

// setup constructs a temporary real store, workflow engine, and HTTP server for request tests.
func setup(t *testing.T) *Server {
	t.Helper()
	s, e := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return New(s, workflow.New(s, github.CLI{}, t.TempDir()), "secret")
}

// TestLocalControlRejectsHostAndOrigin verifies the control API rejects non-loopback hosts and mismatched browser origins.
func TestLocalControlRejectsHostAndOrigin(t *testing.T) {
	s := setup(t)
	for _, c := range []struct{ host, origin string }{{"attacker.example", ""}, {"localhost", "https://attacker.example"}} {
		r := httptest.NewRequest("GET", "http://localhost/api/v1/health", nil)
		r.Host = c.host
		r.Header.Set("Origin", c.origin)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
}

// TestProviderMutationIdempotencyAndVersionConflict verifies mutation retries replay responses and stale provider versions are rejected.
func TestProviderMutationIdempotencyAndVersionConflict(t *testing.T) {
	s := setup(t)
	body := []byte(`{"id":"fake","name":"Fake","kind":"acp","command":["sh"],"capabilities":["code"],"quota":"unknown"}`)
	send := func(b []byte, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/api/v1/providers", bytes.NewReader(b))
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	a := send(body, "one")
	b := send(body, "one")
	if a.Code != 201 || b.Code != 201 || a.Body.String() != b.Body.String() {
		t.Fatalf("%d %s / %d %s", a.Code, a.Body, b.Code, b.Body)
	}
	if send([]byte(`{}`), "one").Code != 409 {
		t.Fatal("key reuse accepted")
	}
	r := httptest.NewRequest("PATCH", "http://localhost/api/v1/providers/fake", bytes.NewReader(body))
	r.Header.Set("Idempotency-Key", "two")
	r.Header.Set("If-Match", "99")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("stale %d %s", w.Code, w.Body)
	}
}

// TestSignedWebhookDeduplicatesEligibleIssue verifies repeated signed issue deliveries create only one eligible task.
func TestSignedWebhookDeduplicatesEligibleIssue(t *testing.T) {
	s := setup(t)
	s.Store.Put(context.Background(), "projects", "p", model.Project{ID: "p", Repo: "o/r"}, 0)
	payload := map[string]any{"action": "labeled", "repository": map[string]any{"full_name": "o/r"}, "issue": map[string]any{"number": 4, "title": "work", "body": "do it", "state": "open", "updated_at": "now", "labels": []any{map[string]any{"name": "khan:plan"}}}}
	b, _ := json.Marshal(payload)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(b)
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "http://public/webhooks/github", bytes.NewReader(b))
		r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		r.Header.Set("X-GitHub-Delivery", "delivery")
		r.Header.Set("X-GitHub-Event", "issues")
		w := httptest.NewRecorder()
		s.WebhookHandler().ServeHTTP(w, r)
		if w.Code != 202 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	tasks, e := s.Store.List(context.Background(), "tasks")
	if e != nil || len(tasks) != 1 {
		t.Fatalf("%d %v", len(tasks), e)
	}
	r := httptest.NewRequest("GET", "http://public/api/v1/tasks", nil)
	w := httptest.NewRecorder()
	s.WebhookHandler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("control API publicly exposed")
	}
}

// TestSettingsRejectZeroCapacity verifies settings cannot disable execution by setting slot capacity to zero.
func TestSettingsRejectZeroCapacity(t *testing.T) {
	s := setup(t)
	s.Store.Put(context.Background(), "settings", "global", map[string]int{"global_slots": 4, "project_slots": 2, "reconciliation_seconds": 300}, 0)
	r := httptest.NewRequest("PATCH", "http://localhost/api/v1/settings", bytes.NewReader([]byte(`{"global_slots":0,"project_slots":2,"reconciliation_seconds":300}`)))
	r.Header.Set("Idempotency-Key", "invalid-settings")
	r.Header.Set("If-Match", "1")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 422 {
		t.Fatalf("invalid capacity accepted: %d", w.Code)
	}
}

// TestEventReplayHonorsCursor verifies SSE resumes after the supplied event ID instead of replaying earlier records.
func TestEventReplayHonorsCursor(t *testing.T) {
	s := setup(t)
	s.Store.Put(context.Background(), "settings", "one", map[string]int{"x": 1}, 0)
	s.Store.Put(context.Background(), "settings", "two", map[string]int{"x": 2}, 0)
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "http://localhost/api/v1/events?after=1", nil).WithContext(ctx)
	w := &cancelWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	s.Handler().ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "id: 1\n") || !strings.Contains(w.Body.String(), "id: 2\n") {
		t.Fatal(w.Body.String())
	}
}

type cancelWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

// Flush flushes recorded SSE output and cancels the fixture request to end its stream.
func (w *cancelWriter) Flush() { w.ResponseRecorder.Flush(); w.cancel() }

// TestResumePreservesPendingPlannerQuestions verifies task resumption restores the questions stage instead of starting coding prematurely.
func TestResumePreservesPendingPlannerQuestions(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	task := model.Task{ID: "q", Stage: "questions", Plan: &model.Plan{Questions: []model.Question{{ID: "q1", Text: "Which callback?"}}}}
	s.Store.Put(ctx, "tasks", task.ID, task, 0)
	send := func(action, key, version string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/api/v1/tasks/q/"+action, bytes.NewReader([]byte(`{}`)))
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("If-Match", version)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := send("pause", "pause-q", "1"); w.Code != 200 {
		t.Fatal(w.Body)
	}
	w := send("resume", "resume-q", "2")
	var r model.Record
	if json.Unmarshal(w.Body.Bytes(), &r) != nil {
		t.Fatal(w.Body)
	}
	resumed, _ := store.Decode[model.Task](r)
	if resumed.Stage != "questions" {
		t.Fatalf("questions bypassed: %s", resumed.Stage)
	}
}

// TestNativeQuestionAnswersRequireCurrentVersion verifies user answers require a current question-set version.
func TestNativeQuestionAnswersRequireCurrentVersion(t *testing.T) {
	s := setup(t)
	set := model.QuestionSet{ID: "native", Status: "pending", Questions: []model.Question{{ID: "db", Text: "Which database?"}}}
	s.Store.Put(context.Background(), "questions", set.ID, set, 0)
	r := httptest.NewRequest("POST", "http://localhost/api/v1/questions/native/answers", bytes.NewReader([]byte(`{"answers":{"db":"SQLite"}}`)))
	r.Header.Set("Idempotency-Key", "native-answer")
	r.Header.Set("If-Match", "1")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	record, _ := s.Store.Get(context.Background(), "questions", set.ID)
	answered, _ := store.Decode[model.QuestionSet](record)
	if answered.Status != "answered" || answered.Answers["db"] != "SQLite" {
		t.Fatalf("%+v", answered)
	}
}
