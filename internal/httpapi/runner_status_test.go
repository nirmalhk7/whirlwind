package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"whirlwind/internal/model"
)

func TestRunnerStatusReportsOnlyRunningUnfinishedAttempts(t *testing.T) {
	for _, test := range []struct {
		name, status   string
		finished, want bool
	}{
		{"running ideation", "running", false, true},
		{"completed", "complete", true, false},
		{"interrupted", "interrupted", false, false},
		{"cancelled", "cancelled", false, false},
		{"queued", "queued", false, false},
		{"legacy unknown", "", false, false},
		{"finished with stale status", "running", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := setup(t)
			if err := s.Store.Put(context.Background(), "sessions", "attempt", model.Attempt{Status: test.status, Finished: test.finished, Task: "ideation:p"}, 0); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "http://localhost/api/v1/runners/status", nil)
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, req)
			if response.Code != 200 {
				t.Fatalf("%d %s", response.Code, response.Body)
			}
			var result struct {
				Processing bool `json:"processing"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Processing != test.want {
				t.Fatalf("processing=%v want %v", result.Processing, test.want)
			}
		})
	}
}
