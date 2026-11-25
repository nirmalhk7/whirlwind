package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"whirlwind/internal/store"
)

func TestRoleQueueHTTPOnlyDispatchesValidLocalAdmissionRequests(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, origin string
		want                     int
		calls                    int
	}{
		{"admit", "pop", "{}", "", http.StatusOK, 1},
		{"bad version", "push", `{"version":2}`, "", http.StatusConflict, 1},
		{"blocked origin", "pop", "{}", "https://example.invalid", http.StatusForbidden, 0},
		{"worker settlement", "ack", "{}", "", http.StatusNotFound, 0},
		{"unknown fields", "pop", `{"receipt":"anything"}`, "", http.StatusBadRequest, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := roleQueueHTTP(func(_ context.Context, queue, action string, request RoleQueueRequest) (RoleQueueAdmission, error) {
				calls++
				if queue != "planner_tasks" || action != tc.path {
					t.Fatalf("unexpected route %s/%s", queue, action)
				}
				if request.Version != 0 {
					return RoleQueueAdmission{}, store.ErrConflict
				}
				return RoleQueueAdmission{PlanID: "p", Version: 1, Status: "scheduled"}, nil
			})
			req := httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/queues/planner_tasks/"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", tc.origin)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			if response.Code != tc.want || calls != tc.calls || strings.Contains(response.Body.String(), "token") {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}
