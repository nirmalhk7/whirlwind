package workflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/provider"
)

// Integration discovery runs in the project, not in ideation scratch; it must
// never spill model inputs into a read-only checkout.
func TestIntegrationDiscoveryDoesNotWriteInputIntoProject(t *testing.T) {
	e, task, repo := testEngine(t)
	project, _ := e.Project(context.Background(), task.ProjectID)
	run := &agentRun{Engine: e, ctx: context.Background(), project: project, task: task, input: provider.Input{
		Role: "ideate", Purpose: "integration_context", CWD: repo, ReadOnly: true, Prompt: strings.Repeat("frozen evidence\n", 4000),
	}}
	if err := run.buildContextPack(); err == nil {
		t.Fatal("integration context bypassed its normal input limit")
	}
	files, err := filepath.Glob(filepath.Join(repo, "ideation-input-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("read-only discovery wrote project files: %v", files)
	}
}
