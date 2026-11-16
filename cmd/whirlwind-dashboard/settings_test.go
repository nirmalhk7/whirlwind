package main

import (
	"context"
	"path/filepath"
	"testing"

	"whirlwind/internal/control"
	"whirlwind/internal/store"
)

func TestDashboardLoadsRunnerStopStateOnlineAndOffline(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	db, err := store.Open(filepath.Join(directory, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Put(ctx, "settings", "global", map[string]bool{"stopped": true}, 0); err != nil {
		t.Fatal(err)
	}
	server, err := control.Listen(control.SocketPath(directory), control.SupervisorDispatcher{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	for _, mode := range []string{"online", "offline"} {
		if mode == "offline" {
			if err := server.Close(); err != nil {
				t.Fatal(err)
			}
		}
		snapshot, err := loadSnapshot(ctx, directory)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot["settings"]) != 1 || snapshot["settings"][0].ID != "global" {
			t.Errorf("%s dashboard lost saved runner stop state", mode)
		}
	}
}
