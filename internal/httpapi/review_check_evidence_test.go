package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"whirlwind/internal/model"
)

const reviewAttemptID = "review-attempt"

// Without both correlation and read-only resource access, broker provenance would be hidden outside raw SQLite.
func TestReviewCheckEvidenceVisibleThroughSessionAndResource(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "sessions", reviewAttemptID, model.Attempt{AttemptID: reviewAttemptID}, 0); err != nil {
		t.Fatal(err)
	}
	check := map[string]any{"attempt_id": reviewAttemptID, "head": "reviewed-commit", "execution_profile": "effective-profile", "check_id": "tests"}
	if err := s.Store.Put(ctx, "review_checks", "check-result", check, 0); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/sessions/" + reviewAttemptID + "/evidence", "/review_checks/check-result"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1"+path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "reviewed-commit") || !strings.Contains(w.Body.String(), "effective-profile") {
			t.Fatalf("check provenance inaccessible: %s %d %s", path, w.Code, w.Body)
		}
	}
}
