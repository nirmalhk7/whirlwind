package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type settingsFailureDispatcher struct {
	base    control.Dispatcher
	message string
}

func (d settingsFailureDispatcher) Handle(ctx context.Context, request control.Request) (control.Response, error) {
	if request.Operation == "inspect.records" && request.ResourceID == "settings" {
		return control.Response{}, errors.New(d.message)
	}
	return d.base.Handle(ctx, request)
}

func TestDashboardReadsSavedSettingsWithLegacySupervisor(t *testing.T) {
	for _, test := range []struct {
		name    string
		stopped bool
	}{{"running", false}, {"stopped", true}} {
		t.Run(test.name, func(t *testing.T) {
			db, directory := legacySettingsFixture(t, test.stopped)
			snapshot, err := loadSnapshot(context.Background(), directory)
			if err != nil {
				t.Fatal(err)
			}
			assertLegacySettingsSnapshot(t, snapshot, test.stopped)
			saved, err := db.Get(context.Background(), "settings", "global")
			if err != nil || saved.Version != 1 {
				t.Fatal("inspection wrote saved settings")
			}
		})
	}
}

func legacySettingsFixture(t *testing.T, stopped bool) (*store.Store, string) {
	t.Helper()
	directory := t.TempDir()
	db, err := store.Open(filepath.Join(directory, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Put(context.Background(), "settings", "global", map[string]bool{"stopped": stopped}, 0); err != nil {
		t.Fatal(err)
	}
	project := model.Project{ID: "saved-project", Name: "Saved project"}
	if err := db.Put(context.Background(), "projects", project.ID, project, 0); err != nil {
		t.Fatal(err)
	}
	server, err := control.Listen(control.SocketPath(directory), settingsFailureDispatcher{
		base: control.SupervisorDispatcher{DB: db.DB}, message: `resource kind "settings" is not available through local inspection`,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return db, directory
}

func assertLegacySettingsSnapshot(t *testing.T, snapshot control.Snapshot, stopped bool) {
	t.Helper()
	if len(snapshot["projects"]) != 1 || snapshot["projects"][0].ID != "saved-project" {
		t.Fatal("legacy supervisor lost saved project")
	}
	if len(snapshot["settings"]) != 1 {
		t.Fatal("saved settings missing")
	}
	var value struct {
		Stopped bool `json:"stopped"`
	}
	if err := json.Unmarshal(snapshot["settings"][0].Data, &value); err != nil {
		t.Fatal(err)
	}
	if value.Stopped != stopped || snapshot["settings"][0].Version != 1 {
		t.Fatal("dashboard changed saved stop state")
	}
	if len(snapshot["events"]) != 2 {
		t.Fatal("legacy settings rejection prevented event inspection")
	}
	if string(snapshot["supervisor"][0].Data) != `{"online":true}` {
		t.Fatal("reachable legacy supervisor shown offline")
	}
}

func TestDashboardDoesNotHideSettingsInspectionFailures(t *testing.T) {
	directory := t.TempDir()
	db, err := store.Open(filepath.Join(directory, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	server, err := control.Listen(control.SocketPath(directory), settingsFailureDispatcher{
		base: control.SupervisorDispatcher{DB: db.DB}, message: "settings storage failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if _, err := loadSnapshot(context.Background(), directory); err == nil || !strings.Contains(err.Error(), "settings storage failed") {
		t.Fatalf("settings failure hidden: %v", err)
	}
}
