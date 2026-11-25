package settingsfile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"whirlwind/internal/store"
)

const editorYAML = "# Original startup comment\nversion: 1\nworkers:\n  global_slots: 4\nproviders: []\nagents: {}\n"

func editorSource(t *testing.T) *Source {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	path := filepath.Join(t.TempDir(), "custom.yaml")
	if err := os.WriteFile(path, []byte(editorYAML), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Attach(context.Background(), s, path); err != nil {
		t.Fatal(err)
	}
	return s.Source.(*Source)
}

func TestStartupSnapshotSurvivesFileRefreshAndSave(t *testing.T) {
	s := editorSource(t)
	startup := s.Snapshot()
	if startup.Content != editorYAML || startup.Path != s.path || startup.LoadedAt == "" {
		t.Fatalf("wrong startup snapshot: %+v", startup)
	}
	external := "# Changed on disk\nversion: 1\nworkers: {global_slots: 6}\nproviders: []\nagents: {}\n"
	if err := os.WriteFile(s.path, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := s.File()
	if err != nil || current.Content != external {
		t.Fatalf("current file: %+v %v", current, err)
	}
	next := "# Preserve edited formatting\nversion: 1\nworkers: {global_slots: 8}\nproviders: []\nagents: {}\n"
	err = s.SaveFile(context.Background(), next, current.Revision, func(changes []store.Change) error {
		return s.store.Write(store.WithoutSettingsSource(context.Background()), changes, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(s.path)
	if string(got) != next {
		t.Fatalf("save reformatted YAML: %s", got)
	}
	if s.Snapshot() != startup {
		t.Fatal("startup snapshot changed")
	}
	record, _ := s.store.Get(context.Background(), "settings", "global")
	if string(record.Data) == "" {
		t.Fatal("runtime settings missing")
	}
	var settings struct {
		GlobalSlots int `json:"global_slots"`
	}
	if err := json.Unmarshal(record.Data, &settings); err != nil || settings.GlobalSlots != 8 {
		t.Fatalf("save not applied: %s %v", record.Data, err)
	}
}

func TestSaveFileRejectsStaleAndInvalidEditsAndRollsBackFailedCommit(t *testing.T) {
	s := editorSource(t)
	current, _ := s.File()
	commit := func([]store.Change) error { t.Fatal("unexpected commit"); return nil }
	if err := s.SaveFile(context.Background(), "workers: [invalid", current.Revision, commit); err == nil {
		t.Fatal("accepted invalid YAML")
	}
	if err := s.SaveFile(context.Background(), editorYAML, "stale", commit); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale edit: %v", err)
	}
	failure := errors.New("receipt failure")
	next := "# New\n" + editorYAML
	if err := s.SaveFile(context.Background(), next, current.Revision, func([]store.Change) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("commit: %v", err)
	}
	got, _ := os.ReadFile(s.path)
	if string(got) != editorYAML {
		t.Fatal("failed save changed file")
	}
	if err := os.WriteFile(s.path, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	broken, _ := s.File()
	if err := s.SaveFile(context.Background(), editorYAML, broken.Revision, func(changes []store.Change) error {
		return s.store.Write(store.WithoutSettingsSource(context.Background()), changes, nil)
	}); err != nil {
		t.Fatalf("could not repair invalid disk file: %v", err)
	}
}

func TestSettingsVersionPreconditionsAreReadOnlyAndFenceApproval(t *testing.T) {
	s := editorSource(t)
	ctx := context.Background()
	const configuration = "version: 1\nworkers: {global_slots: 4}\nproviders: [{id: fixture, name: Fixture, kind: unavailable}]\nagents: {}\n"
	if err := os.WriteFile(s.path, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	provider, err := s.store.Get(ctx, "providers", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	newDecision := func(id string) (context.Context, *store.Batch) {
		t.Helper()
		staged, batch := store.Stage(ctx)
		if err := s.store.Write(staged, []store.Change{
			store.VersionPrecondition("providers", provider.ID, provider.Version),
			store.VersionPrecondition("providers", provider.ID, provider.Version),
			{Kind: "approval_fixture", ID: id, Value: true},
		}, nil); err != nil {
			t.Fatal(err)
		}
		current, err := s.store.Get(staged, "providers", provider.ID)
		if err != nil || current.Version != provider.Version || string(current.Data) != string(provider.Data) {
			t.Fatalf("precondition changed the staged provider: %+v %v", current, err)
		}
		return staged, batch
	}
	staged, batch := newDecision("current")
	if err := batch.Commit(staged, s.store, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Get(ctx, "approval_fixture", "current"); err != nil {
		t.Fatal(err)
	}
	if actual, err := os.ReadFile(s.path); err != nil || string(actual) != configuration {
		t.Fatalf("approval rewrote settings: %q %v", actual, err)
	}
	staged, batch = newDecision("stale")
	const updated = "version: 1\nworkers: {global_slots: 4}\nproviders: [{id: fixture, name: Renamed, kind: unavailable}]\nagents: {}\n"
	if err := os.WriteFile(s.path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	if err := batch.Commit(staged, s.store, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("approval did not reject a changed provider: %v", err)
	}
	if _, err := s.store.Get(ctx, "approval_fixture", "stale"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stale approval was recorded: %v", err)
	}
	if actual, err := os.ReadFile(s.path); err != nil || string(actual) != updated {
		t.Fatalf("stale approval changed settings: %q %v", actual, err)
	}
}
