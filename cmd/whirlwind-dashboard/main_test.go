package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestDashboardLoadsSameDatabaseWithoutRecovery(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	db, err := store.Open(filepath.Join(data, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, kind := range []string{"tasks", "sessions", "questions"} {
		if err := db.Put(ctx, kind, "saved", map[string]any{"status": "pending", "finished": false, "restarts": 2, "deadline": "2026-11-06T23:05:00Z"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		snapshot, err := loadSnapshot(ctx, data)
		if err != nil {
			t.Fatal(err)
		}
		assertUnchangedSnapshot(t, db, snapshot)
	}
}

func assertUnchangedSnapshot(t *testing.T, db *store.Store, snapshot control.Snapshot) {
	t.Helper()
	for _, kind := range []string{"tasks", "sessions", "questions"} {
		if len(snapshot[kind]) != 1 {
			t.Errorf("dashboard lost saved %s", kind)
			continue
		}
		current, err := db.Get(context.Background(), kind, "saved")
		if err != nil {
			t.Fatal(err)
		}
		if current.Version != 1 || string(current.Data) != string(snapshot[kind][0].Data) {
			t.Errorf("dashboard changed %s on open", kind)
		}
	}
}

func TestDashboardReportsCorruptState(t *testing.T) {
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "whirlwind.db"), []byte("corrupt database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSnapshot(context.Background(), data); err == nil {
		t.Fatal("corrupt state appeared empty")
	}
}

func TestDashboardMissingStateDoesNotCreateDatabase(t *testing.T) {
	data := filepath.Join(t.TempDir(), "not-started")
	if _, err := loadSnapshot(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("dashboard created execution state: %v", err)
	}
}

func TestDashboardRestoresHistoryAndCurrentDecisionsOnlineAndOffline(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	db, err := store.Open(filepath.Join(data, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, kind := range []string{"tasks", "sessions", "questions", "permissions", "plans"} {
		for i := range 105 {
			if err := db.Put(ctx, kind, fmt.Sprintf("%s-old-%03d", kind, i), map[string]any{"project_id": "project", "status": "resolved"}, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, kind := range []string{"questions", "permissions"} {
		if err := db.Put(ctx, kind, kind+"-current", map[string]any{"project_id": "project", "status": "pending", "attempt_id": "current-attempt"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Put(ctx, "sessions", "current-attempt", map[string]any{"project_id": "project", "status": "running", "restart_count": 3}, 0); err != nil {
		t.Fatal(err)
	}

	assertHistory := func(mode string) {
		t.Helper()
		snapshot, err := loadSnapshot(ctx, data)
		if err != nil {
			t.Fatalf("%s inspection: %v", mode, err)
		}
		for _, kind := range []string{"tasks", "sessions", "questions", "permissions", "plans"} {
			minimum := 105
			if kind == "sessions" || kind == "questions" || kind == "permissions" {
				minimum++
			}
			if len(snapshot[kind]) < minimum {
				t.Errorf("%s dashboard restored only %d %s rows; want at least %d", mode, len(snapshot[kind]), kind, minimum)
			}
		}
		for _, current := range []struct{ kind, id, status string }{
			{kind: "questions", id: "questions-current", status: "pending"},
			{kind: "permissions", id: "permissions-current", status: "pending"},
			{kind: "sessions", id: "current-attempt", status: "running"},
		} {
			var saved *model.Record
			for index := range snapshot[current.kind] {
				if snapshot[current.kind][index].ID == current.id {
					saved = &snapshot[current.kind][index]
					break
				}
			}
			if saved == nil {
				t.Errorf("%s dashboard omitted current record %q", mode, current.id)
				continue
			}
			var data map[string]any
			if err := json.Unmarshal(saved.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data["status"] != current.status {
				t.Errorf("%s dashboard changed %s status: got %v, want %s", mode, current.id, data["status"], current.status)
			}
			if current.kind != "sessions" && data["attempt_id"] != "current-attempt" {
				t.Errorf("%s dashboard detached decision %s from paused attempt: %v", mode, current.id, data["attempt_id"])
			}
			if current.kind == "sessions" && data["restart_count"] != float64(3) {
				t.Errorf("%s dashboard changed persisted restart count: %v", mode, data["restart_count"])
			}
		}
	}

	server, err := control.Listen(control.SocketPath(data), control.SupervisorDispatcher{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	assertHistory("online")
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	assertHistory("offline")
}

func TestDashboardPaginationErrorsAndStalledCursorsAreVisible(t *testing.T) {
	for _, test := range []struct {
		name string
		fail func(control.Request) (control.Response, error)
		want string
	}{
		{name: "page error", fail: func(request control.Request) (control.Response, error) {
			return control.Response{RequestID: request.RequestID, Error: "page unavailable"}, nil
		}, want: "page unavailable"},
		{name: "stalled cursor", fail: func(request control.Request) (control.Response, error) {
			return control.Response{Result: control.RecordPage{Kind: request.ResourceID, Cursor: request.EventCursor, NextCursor: request.EventCursor, HasMore: true}}, nil
		}, want: "cursor did not advance"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			data := t.TempDir()
			db, err := store.Open(filepath.Join(data, "whirlwind.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for i := range 100 {
				if err := db.Put(ctx, "tasks", fmt.Sprintf("task-%03d", i), map[string]any{"stage": "plan"}, 0); err != nil {
					t.Fatal(err)
				}
			}
			server, err := control.Listen(control.SocketPath(data), faultyPageDispatcher{base: control.SupervisorDispatcher{DB: db.DB}, fail: test.fail})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			if _, err := loadSnapshot(ctx, data); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("inspection error=%v, want containing %q", err, test.want)
			}
		})
	}
}

type faultyPageDispatcher struct {
	base control.Dispatcher
	fail func(control.Request) (control.Response, error)
}

func (d faultyPageDispatcher) Handle(ctx context.Context, request control.Request) (control.Response, error) {
	if request.Operation == "inspect.records" {
		return d.fail(request)
	}
	return d.base.Handle(ctx, request)
}

func TestDashboardOnlineInspectionHonorsCancellation(t *testing.T) {
	data := t.TempDir()
	db, err := store.Open(filepath.Join(data, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server, err := control.Listen(control.SocketPath(data), control.SupervisorDispatcher{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loadSnapshot(ctx, data); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled online inspection error=%v, want context canceled", err)
	}
}

func TestDashboardEventPaginationRetainsLaterEvents(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	db, err := store.Open(filepath.Join(data, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := range 106 {
		if err := db.Put(ctx, "questions", fmt.Sprintf("question-%03d", i), map[string]any{"status": "resolved"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	server, err := control.Listen(control.SocketPath(data), control.SupervisorDispatcher{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	snapshot, err := loadSnapshot(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot["events"]) < 106 {
		t.Fatalf("online snapshot retained %d events, want all pages", len(snapshot["events"]))
	}
	var last model.Event
	if err := json.Unmarshal(snapshot["events"][len(snapshot["events"])-1].Data, &last); err != nil {
		t.Fatal(err)
	}
	if last.ID <= 100 {
		t.Fatalf("online event cursor stopped before later history: %d", last.ID)
	}
}

func TestDashboardLoadsLargeLegitimateRecordsOverBoundedSocketPages(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	db, err := store.Open(filepath.Join(data, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	largeOutput := strings.Repeat("verification output ", 1400)
	for i := range 80 {
		if err := db.Put(ctx, "verification", fmt.Sprintf("verification-%03d", i), map[string]string{"output": largeOutput}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Put(ctx, "questions", "latest-question", map[string]any{"status": "pending", "attempt_id": "latest-attempt"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "sessions", "latest-attempt", map[string]any{"status": "running", "restart_count": 2}, 0); err != nil {
		t.Fatal(err)
	}
	server, err := control.Listen(control.SocketPath(data), control.SupervisorDispatcher{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	snapshot, err := loadSnapshot(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if !supervisorOnline(snapshot) {
		t.Fatal("large inspection history caused online IPC to fall back to offline database inspection")
	}
	if len(snapshot["verification"]) != 80 || snapshot["verification"][79].ID != "verification-079" {
		t.Fatalf("large verification history was not fully restored: count=%d", len(snapshot["verification"]))
	}
	if len(snapshot["questions"]) == 0 || snapshot["questions"][len(snapshot["questions"])-1].ID != "latest-question" {
		t.Fatal("current paused question was omitted after large verification history")
	}
}

func supervisorOnline(snapshot control.Snapshot) bool {
	if len(snapshot["supervisor"]) != 1 {
		return false
	}
	var status struct {
		Online bool `json:"online"`
	}
	return json.Unmarshal(snapshot["supervisor"][0].Data, &status) == nil && status.Online
}
