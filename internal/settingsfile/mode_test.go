package settingsfile

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const agenticModeYAML = "mode: agentic\n"

func TestModeChangeWaitsForRestartAndCancelsOnlyUnfinishedWork(t *testing.T) {
	s := editorSource(t)
	ctx := context.Background()
	for id, stage := range map[string]string{"pending": "coding", "done": "complete"} {
		if err := s.store.Put(ctx, "tasks", id, model.Task{ID: id, Stage: stage}, 0); err != nil {
			t.Fatal(err)
		}
	}
	next := agenticModeYAML + editorYAML
	if err := os.WriteFile(s.path, []byte(next), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := s.store.Get(ctx, "workflow_config", "global")
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	_ = json.Unmarshal(r.Data, &state)
	if state["active_mode"] != "personal" || state["configured_mode"] != "agentic" || state["restart_required"] != true {
		t.Fatalf("mode activated early: %s", r.Data)
	}
	task, _ := s.store.Get(ctx, "tasks", "pending")
	before, _ := store.Decode[model.Task](task)
	if before.Stage != "coding" {
		t.Fatal("disk edit stopped running task")
	}
	if err := Attach(ctx, s.store, s.path); err != nil {
		t.Fatal(err)
	}
	task, _ = s.store.Get(ctx, "tasks", "pending")
	after, _ := store.Decode[model.Task](task)
	if after.Stage != "cancelled" || after.ResumeStage != "" {
		t.Fatalf("unfinished task resumed: %+v", after)
	}
	done, _ := s.store.Get(ctx, "tasks", "done")
	completed, _ := store.Decode[model.Task](done)
	if completed.Stage != "complete" {
		t.Fatal("completed task changed")
	}
	revision := task.Version
	if err := Attach(ctx, s.store, s.path); err != nil {
		t.Fatal(err)
	}
	task, _ = s.store.Get(ctx, "tasks", "pending")
	if task.Version != revision {
		t.Fatal("same-mode restart cancelled again")
	}
}

func TestYAMLEditorCannotChangeModeButPreservesPendingDiskMode(t *testing.T) {
	s := editorSource(t)
	ctx := context.Background()
	current, _ := s.File()
	if err := s.SaveFile(ctx, agenticModeYAML+editorYAML, current.Revision, func(changes []store.Change) error {
		return s.store.Write(store.WithoutSettingsSource(ctx), changes, nil)
	}); err == nil {
		t.Fatal("UI changed mode")
	}
	if err := os.WriteFile(s.path, []byte(agenticModeYAML+editorYAML), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	r, _ := s.store.Get(ctx, "settings", "global")
	var workers map[string]any
	_ = json.Unmarshal(r.Data, &workers)
	workers["global_slots"] = float64(7)
	if err := s.store.Put(ctx, "settings", "global", workers, r.Version); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(s.path)
	if !strings.Contains(string(raw), "mode: agentic") {
		t.Fatalf("pending mode overwritten: %s", raw)
	}
}

func TestModeActivationTerminallyCancelsOutstandingOperations(t *testing.T) {
	s := editorSource(t)
	ctx := context.Background()
	for kind, value := range map[string]any{
		"operations": map[string]any{"stage": "running"}, "mutations": map[string]any{"stage": "running"},
		"publications": map[string]any{"stage": "running"}, "permissions": map[string]any{"decision": "pending"},
		"questions": map[string]any{"status": "pending"},
	} {
		if err := s.store.Put(ctx, kind, "pending", value, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.ActivateMode(ctx, model.AgenticMode); err != nil {
		t.Fatal(err)
	}
	for kind, field := range map[string]string{"operations": "stage", "mutations": "stage", "publications": "stage", "permissions": "decision", "questions": "status"} {
		r, err := s.store.Get(ctx, kind, "pending")
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		_ = json.Unmarshal(r.Data, &value)
		if value[field] != "cancelled" {
			t.Errorf("%s remains resumable: %s", kind, r.Data)
		}
	}
}
