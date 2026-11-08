package httpapi

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

func TestMutationReceiptFailureRollsBackResource(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err = s.Put(context.Background(), "settings", "global", map[string]int{"global_slots": 4, "project_slots": 2, "reconciliation_seconds": 300}, 0); err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec("CREATE TRIGGER fail_receipt BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	api := New(s, workflow.New(s, nil, t.TempDir()), "")
	r := httptest.NewRequest("PATCH", "http://127.0.0.1/api/v1/settings", strings.NewReader(`{"global_slots":8,"project_slots":2,"reconciliation_seconds":300}`))
	r.Header.Set("Idempotency-Key", "atomic")
	r.Header.Set("If-Match", `"1"`)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)
	record, err := s.Get(context.Background(), "settings", "global")
	if err != nil {
		t.Fatal(err)
	}
	if record.Version != 1 {
		t.Fatalf("mutation committed without replay receipt: %s", record.Data)
	}
}

func TestApprovalRejectsMissingProvenance(t *testing.T) {
	q := apiRequest{}
	task := model.Task{Stage: "approval", ChecksPassed: true, Score: 9, ReviewedCommit: "head"}
	if err := q.approveTask(&task); err == nil {
		t.Fatal("approval accepted without linked check/review evidence")
	}
}

func TestRawApprovalRouteCannotBypassLocalFinalizationEvidence(t *testing.T) {
	q := apiRequest{}
	task := model.Task{ID: "critical", Stage: "approval", Risk: "critical"}
	err := q.approveTask(&task)
	apiErr, ok := err.(*apiError)
	if !ok || apiErr.code != "local_finalization_required" {
		t.Fatalf("raw task action did not require exact local finalization approval: %v", err)
	}
	if task.Stage != "approval" || task.Approved {
		t.Fatalf("raw approval action changed critical task state: %+v", task)
	}
}
func TestAttemptEvidenceReadEndpoints(t *testing.T) {
	api := setup(t)
	ctx := context.Background()
	if err := api.Store.Put(ctx, "sessions", "a", map[string]any{"finished": true}, 0); err != nil {
		t.Fatal(err)
	}
	artifact, err := api.Store.Capture(ctx, "a", "prompt", "text/plain", []byte("exact prompt"), store.MessagePayloadLimit, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/sessions/a/events", "/artifacts/" + artifact.ID, "/artifacts/" + artifact.ID + "/content"} {
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1"+path, nil))
		if w.Code != 200 {
			t.Errorf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/sessions/a/events?limit=501", nil))
	if w.Code != 422 {
		t.Fatal("unbounded evidence page accepted", w.Code)
	}
}

func TestAttemptInspectionLinksOnlyItsEvidence(t *testing.T) {
	api := setup(t)
	ctx := context.Background()
	for _, c := range []store.Change{{Kind: "sessions", ID: "a", Value: map[string]bool{"finished": true}}, {Kind: "verification", ID: "own-verification", Value: map[string]any{"provenance": model.Provenance{AttemptIDs: []string{"a"}}}}, {Kind: "reviews", ID: "own-review", Value: model.Review{Score: 9, Provenance: &model.Provenance{AttemptIDs: []string{"a"}}}}, {Kind: "verification", ID: "unrelated", Value: map[string]any{"attempt_id": "b"}}} {
		if err := api.Store.Put(ctx, c.Kind, c.ID, c.Value, c.Expected); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/sessions/a/evidence", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "own-verification") || !strings.Contains(w.Body.String(), "own-review") || strings.Contains(w.Body.String(), "unrelated") {
		t.Fatal("attempt correlations missing/incorrect", w.Code, w.Body)
	}
}
