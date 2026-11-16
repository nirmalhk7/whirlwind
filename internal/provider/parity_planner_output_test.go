package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"whirlwind/internal/execution"
)

func TestParityReadOnlyPlannerWritesOnlyAssignedOutput(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "assigned-output")
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	client := NewClient(Input{Role: "plan", Purpose: "idea_planning", CWD: root, ReadOnly: true, Execution: &execution.Profile{CWD: root, ReadOnly: true, WriteRoots: []string{output}}}, Hooks{})
	defer func() { _ = client.Close() }()
	for _, tc := range []struct {
		path    string
		allowed bool
	}{{filepath.Join(output, "plan.md"), true}, {filepath.Join(root, "source.go"), false}, {filepath.Join(output, "..", "escape.md"), false}} {
		params, _ := json.Marshal(map[string]string{"path": tc.path, "content": "# Plan\n"})
		_, err := client.Handle(context.Background(), Message{Method: "fs/write_text_file", Params: params})
		if (err == nil) != tc.allowed {
			t.Fatalf("assigned output boundary %s allowed=%v err=%v", tc.path, tc.allowed, err)
		}
	}
}
