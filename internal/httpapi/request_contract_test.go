package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

type requestCase struct {
	method, path, body, version, errorCode string
	status                                 int
}

// TestRequestContracts preserves route precedence and validation order while handlers move apart.
func TestRequestContracts(t *testing.T) {
	s := setup(t)
	set := model.QuestionSet{ID: "cancelled", Status: "cancelled"}
	if err := s.Store.Put(context.Background(), "questions", set.ID, set, 0); err != nil {
		t.Fatal(err)
	}
	cases := []requestCase{
		{method: "GET", path: "/api/v1/health/extra/action", status: 200},
		{method: "GET", path: "/api/v1/health/a/b/c", status: 404, errorCode: "route_not_found"},
		{method: "GET", path: "/api/v1/settings//extra", status: 200},
		{method: "GET", path: "/api/v1/tasks/missing", status: 404, errorCode: "not_found"},
		{method: "POST", path: "/api/v1/tasks/missing/unknown", status: 428, errorCode: "precondition_required"},
		{method: "POST", path: "/api/v1/tasks/missing/unknown", version: "1", status: 404, errorCode: "not_found"},
		{method: "POST", path: "/api/v1/projects//ideation", body: `{}`, status: 422, errorCode: "invalid_input"},
		{method: "POST", path: "/api/v1/questions/cancelled/answers", body: `{"answers":{}}`, version: "99", status: 409, errorCode: "invalid_stage"},
		{method: "PATCH", path: "/api/v1/inbox/missing", version: "1", status: 404, errorCode: "not_found"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path+" "+c.version, func(t *testing.T) { assertRequest(t, s, c) })
	}
}

func assertRequest(t *testing.T, s *Server, c requestCase) {
	t.Helper()
	r := httptest.NewRequest(c.method, "http://localhost"+c.path, strings.NewReader(c.body))
	r.Header.Set("Idempotency-Key", t.Name())
	r.Header.Set("If-Match", c.version)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var response struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != c.status || response.Error.Code != c.errorCode {
		t.Fatalf("got %d %s; want %d %s: %s", w.Code, response.Error.Code, c.status, c.errorCode, w.Body)
	}
}
