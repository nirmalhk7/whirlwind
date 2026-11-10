package httpapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/settingsfile"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

func TestLocalRunnerToggleFlipsPersistedStateAndReplaysOnce(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "settings.yaml")
	if err := settingsfile.Attach(ctx, s.Store, file); err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "active", Stage: "coding", Selection: &model.SelectionAuthorization{CandidateID: "active", SelectedAt: "2026-11-08T00:00:00Z"}}
	if err := s.Store.Put(ctx, "tasks", "active", task, 0); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, false, true} {
		command := LocalCommand{Operation: "runners.toggle", IdempotencyKey: fmt.Sprintf("toggle-%d", i)}
		for retry := 0; retry < 2; retry++ {
			if _, err := s.ExecuteLocal(ctx, command); err != nil {
				t.Fatal(err)
			}
			assertRunnerToggleState(t, s.Store, file, want)
		}
		record, err := s.Store.Get(ctx, "tasks", "active")
		if err != nil {
			t.Fatal(err)
		}
		task, err := store.Decode[model.Task](record)
		stage := "coding"
		if want {
			stage = "paused"
		}
		if err != nil || task.Stage != stage {
			t.Fatalf("toggle did not apply runner lifecycle: %+v (%v)", task, err)
		}
	}
}

func assertRunnerToggleState(t *testing.T, db *store.Store, file string, want bool) {
	t.Helper()
	current, err := db.Get(context.Background(), "settings", "global")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := store.Decode[workflow.Settings](current)
	if err != nil || settings.Stopped != want {
		t.Fatalf("stopped=%v, want %v (%v)", settings.Stopped, want, err)
	}
	raw, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(raw), fmt.Sprintf("stopped: %v", want)) {
		t.Fatalf("toggle not saved to YAML: %s (%v)", raw, err)
	}
}

func TestLocalRunnerToggleUsesLatestAuthoritativeYAML(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "settings.yaml")
	if err := settingsfile.Attach(ctx, s.Store, file); err != nil {
		t.Fatal(err)
	}
	// Edit disk after startup without refreshing the engine or dashboard.
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "stopped: false", "stopped: true", 1)
	if edited == string(raw) {
		t.Fatal("fixture must start with runners enabled")
	}
	if err := os.WriteFile(file, []byte(edited), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecuteLocal(ctx, LocalCommand{Operation: "runners.toggle", IdempotencyKey: "toggle-disk-edit"}); err != nil {
		t.Fatal(err)
	}
	assertRunnerToggleState(t, s.Store, file, false)
}
