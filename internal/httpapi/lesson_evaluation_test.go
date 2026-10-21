package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"whirlwind/internal/model"
)

func TestLessonEvaluationRequiresConfiguredEndpoint(t *testing.T) {
	server := setup(t)
	if err := server.Store.Put(context.Background(), "projects", "p", model.Project{ID: "p"}, 0); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "http://localhost/api/v1/projects/p/lesson-search-evaluation", strings.NewReader(`{"fixtures":[{"role":"code","query":"retries","relevant":[]}]}`))
	request.Header.Set("If-Match", "1")
	request.Header.Set("Idempotency-Key", "lesson-evaluation")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != 422 {
		t.Fatalf("unconfigured evaluation: %d %s", response.Code, response.Body)
	}
}
