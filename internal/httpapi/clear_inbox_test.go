package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const resolvedNotificationID = "already-resolved"

func TestClearInboxResolvesEveryNotificationAndRetainsPendingDecisions(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	changes := []store.Change{}
	for i := 0; i < 505; i++ {
		id := fmt.Sprintf("notice-%03d", i)
		changes = append(changes, store.Change{Kind: "inbox", ID: id, Value: model.Notification{ID: id, Kind: "ideation", Message: "old failure"}})
	}
	changes = append(changes, store.Change{Kind: "inbox", ID: resolvedNotificationID, Value: model.Notification{ID: resolvedNotificationID, Resolved: true}})
	changes = append(changes, store.Change{Kind: "questions", ID: "question", Value: map[string]any{"status": "pending"}})
	changes = append(changes, store.Change{Kind: "permissions", ID: "permission", Value: map[string]any{"decision": "pending"}})
	if err := s.Store.Write(ctx, changes, nil); err != nil {
		t.Fatal(err)
	}
	send := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://localhost/api/v1/inbox/clear", strings.NewReader(`{}`))
		req.Header.Set(idempotencyKeyHeader, key)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		return response
	}
	response := send("clear-inbox")
	assertInitialInboxCleared(t, s.Store, response)
	for _, kind := range []string{"questions", "permissions"} {
		assertPendingDecisionsUnchanged(t, s.Store, ctx, kind)
	}
	assertInboxReplayPreservesNewMessage(t, s.Store, send, response)
	response = send("clear-again")
	assertSecondInboxClear(t, response)
}

func assertInitialInboxCleared(t *testing.T, db *store.Store, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
	var result struct {
		Resolved int `json:"resolved"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Resolved != 505 {
		t.Fatalf("cleared count: %+v %v", result, err)
	}
	records, err := db.List(context.Background(), "inbox")
	if err != nil || len(records) != 506 {
		t.Fatalf("history removed: %d %v", len(records), err)
	}
	for _, r := range records {
		notice, err := store.Decode[model.Notification](r)
		if err != nil || !notice.Resolved {
			t.Fatalf("notification still pending: %+v %v", notice, err)
		}
		if r.ID == resolvedNotificationID && r.Version != 1 {
			t.Fatal("already-resolved notification rewritten")
		}
	}
}

func assertPendingDecisionsUnchanged(t *testing.T, db *store.Store, ctx context.Context, kind string) {
	t.Helper()
	records, err := db.List(ctx, kind)
	if err != nil || len(records) != 1 || records[0].Version != 1 {
		t.Fatalf("%s modified: %+v %v", kind, records, err)
	}
}

func assertInboxReplayPreservesNewMessage(t *testing.T, db *store.Store, send func(string) *httptest.ResponseRecorder, response *httptest.ResponseRecorder) {
	t.Helper()
	if err := db.Put(context.Background(), "inbox", "new", model.Notification{ID: "new", Message: "arrived later"}, 0); err != nil {
		t.Fatal(err)
	}
	replay := send("clear-inbox")
	if replay.Code != 200 || replay.Body.String() != response.Body.String() {
		t.Fatalf("replay changed: %d %s", replay.Code, replay.Body)
	}
	record, _ := db.Get(context.Background(), "inbox", "new")
	notice, _ := store.Decode[model.Notification](record)
	if notice.Resolved {
		t.Fatal("retry cleared a message arriving after original request")
	}
}

func assertSecondInboxClear(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("second clear: %d %s", response.Code, response.Body)
	}
	var result struct {
		Resolved int `json:"resolved"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Resolved != 1 {
		t.Fatalf("second count: %+v %v", result, err)
	}
}
