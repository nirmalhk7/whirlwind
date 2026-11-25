package settingsfile

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestYAMLRegistrationOptOutLeavesOnlySetup(t *testing.T) {
	source := editorSource(t)
	repository := t.TempDir()
	if out, err := exec.Command("git", "init", repository).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	paths, _ := json.Marshal([]string{repository})
	content := strings.Replace(editorYAML, "  global_slots: 4\n", "  global_slots: 4\n  ideation:\n    on_registration: false\n    cron: \"\"\n", 1)
	content += "projects: " + string(paths) + "\n"
	if err := os.WriteFile(source.path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := source.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	operations, err := source.store.List(store.WithoutSettingsSource(t.Context()), "operations")
	if err != nil || len(operations) != 1 {
		t.Fatalf("expected only durable project setup with automatic ideation disabled: %d operations, %v", len(operations), err)
	}
	op, err := store.Decode[model.Operation](operations[0])
	if err != nil || op.Kind != "project_setup" || op.Stage != "queued" {
		t.Fatalf("registration stopped preparing the project: %+v %v", op, err)
	}
}
