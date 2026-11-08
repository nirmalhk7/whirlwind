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
	for _, kind := range []string{"projects", "tasks", "directions", "sessions", "questions", "permissions", "verification", "reviews", "inbox", "plans"} {
		if err := db.Put(ctx, kind, "existing", map[string]any{"status": "pending", "deadline": "2026-11-06T23:05:00Z", "restarts": 2}, 0); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		reader, err := store.OpenReadOnly(path)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := Inspect(ctx, reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"projects", "tasks", "directions", "sessions", "questions", "permissions", "verification", "reviews", "inbox", "plans"} {
			if len(snapshot[kind]) != 1 || snapshot[kind][0].ID != "existing" || snapshot[kind][0].Version != 1 {
				t.Errorf("lost or changed %s state after reopening", kind)
			}
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
