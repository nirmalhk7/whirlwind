package control

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"whirlwind/internal/store"
)

func TestInspectionRestoresExistingPlanAndPendingDecisions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	expected := existingInspectionRecords()
	for _, record := range expected {
		err := db.Put(ctx, record.kind, record.id, map[string]any{"status": "pending", "deadline": "2026-11-06T23:05:00Z", "restarts": 2}, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		assertExistingInspection(t, ctx, path, expected)
	}
}

type inspectionFixtureRecord struct {
	kind, id string
}

func existingInspectionRecords() []inspectionFixtureRecord {
	var expected []inspectionFixtureRecord
	for _, kind := range []string{"projects", "execution_plans", "directions", "sessions", "questions", "permissions", "verification", "reviews", "inbox", "plan_versions"} {
		id := "existing"
		if kind == "plan_versions" {
			id = "existing:1"
		}
		expected = append(expected, inspectionFixtureRecord{kind, id})
	}
	return expected
}

func assertExistingInspection(t *testing.T, ctx context.Context, path string, expected []inspectionFixtureRecord) {
	t.Helper()
	reader, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Inspect(ctx, reader)
	if err := errors.Join(err, reader.Close()); err != nil {
		t.Fatal(err)
	}
	for _, record := range expected {
		rows := snapshot[record.kind]
		if len(rows) != 1 || rows[0].ID != record.id || rows[0].Version != 1 {
			t.Errorf("lost or changed %s state after reopening", record.kind)
		}
	}
}

func TestInspectionReportsCancelledRead(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Inspect(ctx, db); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled inspection reported success: %v", err)
	}
}
