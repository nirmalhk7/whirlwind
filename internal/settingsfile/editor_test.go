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
