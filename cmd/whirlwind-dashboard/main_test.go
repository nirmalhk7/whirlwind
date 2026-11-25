package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const dashboardCurrentAttempt = "current-attempt"

func openDashboardFixture(t *testing.T) (string, *store.Store) {
	t.Helper()
	data := t.TempDir()
	db, err := store.Open(filepath.Join(data, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close dashboard fixture database: %v", err)
		}
	})
	return data, db
}

func startDashboardFixtureServer(t *testing.T, data string, dispatcher control.Dispatcher) *control.LocalServer {
	t.Helper()
	server, err := control.Listen(control.SocketPath(data), dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close dashboard fixture socket: %v", err)
		}
	})
	return server
}

func TestDashboardLoadsSameDatabaseWithoutRecovery(t *testing.T) {
	ctx := context.Background()
	data, db := openDashboardFixture(t)
	for _, kind := range []string{"execution_plans", "sessions", "questions"} {
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
	for _, kind := range []string{"execution_plans", "sessions", "questions"} {
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
	data, db := openDashboardFixture(t)
	seedDashboardHistory(t, ctx, db)

	server := startDashboardFixtureServer(t, data, control.SupervisorDispatcher{DB: db.DB})
	assertDashboardHistory(t, ctx, data, "online")
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	assertDashboardHistory(t, ctx, data, "offline")
}

func seedDashboardHistory(t *testing.T, ctx context.Context, db *store.Store) {
	t.Helper()
	for _, kind := range []string{"execution_plans", "sessions", "questions", "permissions", "plan_versions"} {
		for i := range 105 {
			id := fmt.Sprintf("%s-old-%03d", kind, i)
			if kind == "plan_versions" {
				id = fmt.Sprintf("execution_plans-old-%03d:1", i)
			}
			if err := db.Put(ctx, kind, id, map[string]any{"project_id": "project", "status": "resolved"}, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, kind := range []string{"questions", "permissions"} {
		if err := db.Put(ctx, kind, kind+"-current", map[string]any{"project_id": "project", "status": "pending", "attempt_id": dashboardCurrentAttempt}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Put(ctx, "sessions", dashboardCurrentAttempt, map[string]any{"project_id": "project", "status": "running", "restart_count": 3}, 0); err != nil {
		t.Fatal(err)
	}
}

func assertDashboardHistory(t *testing.T, ctx context.Context, data, mode string) {
	t.Helper()
	snapshot, err := loadSnapshot(ctx, data)
	if err != nil {
		t.Fatalf("%s inspection: %v", mode, err)
	}
	for _, group := range []struct {
		kind    string
		minimum int
	}{
		{"execution_plans", 105},
		{"sessions", 106},
		{"questions", 106},
		{"permissions", 106},
		{"plan_versions", 105},
	} {
		if count := len(snapshot[group.kind]); count < group.minimum {
			t.Errorf("%s dashboard restored only %d %s rows; want at least %d", mode, count, group.kind, group.minimum)
		}
	}
	assertDashboardHistoryRecord(t, mode, snapshot["questions"], "questions-current", "pending", "attempt_id", dashboardCurrentAttempt)
	assertDashboardHistoryRecord(t, mode, snapshot["permissions"], "permissions-current", "pending", "attempt_id", dashboardCurrentAttempt)
	assertDashboardHistoryRecord(t, mode, snapshot["sessions"], dashboardCurrentAttempt, "running", "restart_count", float64(3))
}

func assertDashboardHistoryRecord(t *testing.T, mode string, records []model.Record, id, status, field string, want any) {
	t.Helper()
	index := slices.IndexFunc(records, func(record model.Record) bool { return record.ID == id })
	if index == -1 {
		t.Errorf("%s dashboard omitted current record %q", mode, id)
		return
	}
	var data map[string]any
	if err := json.Unmarshal(records[index].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["status"] != status {
		t.Errorf("%s dashboard changed %q status: got %v, want %s", mode, id, data["status"], status)
	}
	if data[field] != want {
		t.Errorf("%s dashboard changed %q %s: got %v, want %v", mode, id, field, data[field], want)
	}
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
			data, db := openDashboardFixture(t)
			for i := range 100 {
				if err := db.Put(ctx, "execution_plans", fmt.Sprintf("task-%03d", i), map[string]any{"stage": "plan"}, 0); err != nil {
					t.Fatal(err)
				}
			}
			startDashboardFixtureServer(t, data, faultyPageDispatcher{base: control.SupervisorDispatcher{DB: db.DB}, fail: test.fail})
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
	data, db := openDashboardFixture(t)
	startDashboardFixtureServer(t, data, control.SupervisorDispatcher{DB: db.DB})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loadSnapshot(ctx, data); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled online inspection error=%v, want context canceled", err)
	}
}

func TestDashboardEventPaginationRetainsLaterEvents(t *testing.T) {
	ctx := context.Background()
	data, db := openDashboardFixture(t)
	for i := range 106 {
		if err := db.Put(ctx, "questions", fmt.Sprintf("question-%03d", i), map[string]any{"status": "resolved"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	startDashboardFixtureServer(t, data, control.SupervisorDispatcher{DB: db.DB})
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
	data, db := openDashboardFixture(t)
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
	startDashboardFixtureServer(t, data, control.SupervisorDispatcher{DB: db.DB})
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
