package control

import (
	"context"
	"path/filepath"
	"testing"

	"whirlwind/internal/store"
)

func TestInspectionIncludesSavedRunnerStopState(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Put(ctx, "settings", "global", map[string]bool{"stopped": true}, 0); err != nil {
		t.Fatal(err)
	}
	dispatcher := SupervisorDispatcher{DB: db.DB}
	checks := map[string]func() (Snapshot, error){
		"online":  func() (Snapshot, error) { return dispatcher.snapshot(ctx, Request{}) },
		"offline": func() (Snapshot, error) { return dispatcher.Snapshot(ctx, 100) },
		"store":   func() (Snapshot, error) { return Inspect(ctx, db) },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			snapshot, err := check()
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot["settings"]) != 1 || snapshot["settings"][0].ID != "global" {
				t.Fatal("saved runner stop state missing from inspection")
			}
		})
	}
}
