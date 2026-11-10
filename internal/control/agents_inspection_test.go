package control

import (
	"context"
	"path/filepath"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestOfflineInspectionIncludesConfiguredAgentQueues(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Put(ctx, "agents", "code", model.AgentConfig{Role: "code", Chain: []model.AgentChoice{{Provider: "codex", Model: "chosen-model"}}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "project_status", "configured", []model.ProjectStatus{{Path: "/missing", Status: "ERR"}}, 0); err != nil {
		t.Fatal(err)
	}
	snapshot, err := (SupervisorDispatcher{DB: db.DB}).Snapshot(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot["agents"]) != 1 || snapshot["agents"][0].ID != "code" {
		t.Fatal("saved configured agents missing from dashboard inspection")
	}
	if len(snapshot["project_status"]) != 1 {
		t.Fatal("configured project health missing from dashboard inspection")
	}
}

func TestInspectionIncludesConfiguredProviders(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Put(ctx, "providers", "cursor", model.Provider{ID: "cursor", Name: "Cursor", Kind: "acp"}, 0); err != nil {
		t.Fatal(err)
	}
	dispatcher := SupervisorDispatcher{DB: db.DB}
	checks := map[string]func() (Snapshot, error){
		"offline": func() (Snapshot, error) { return dispatcher.Snapshot(ctx, 100) },
		"online":  func() (Snapshot, error) { return dispatcher.snapshot(ctx, Request{}) },
		"store":   func() (Snapshot, error) { return Inspect(ctx, db) },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			snapshot, err := check()
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot["providers"]) != 1 || snapshot["providers"][0].ID != "cursor" {
				t.Fatal("configured agent missing from inspection")
			}
		})
	}
}
