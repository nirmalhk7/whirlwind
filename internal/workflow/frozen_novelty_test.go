package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/provider"
)

func TestFrozenGenerationAndNoveltyPreserveCompleteInput(t *testing.T) {
	for _, purpose := range []string{"generation", "novelty:0"} {
		t.Run(purpose, func(t *testing.T) {
			verifyFrozenPurpose(t, purpose)
		})
	}
}

func verifyFrozenPurpose(t *testing.T, purpose string) {
	t.Helper()
	e, task, _ := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	p.Policy.ContextInputTokens = 8192
	scratch := t.TempDir()
	full := "Never publish.\n" + strings.Repeat("historical evidence 界\n", 4000) + "\nFINAL CONSTRAINT"
	run := agentRun{Engine: e, ctx: ctx, project: p, task: task, input: provider.Input{CWD: scratch, Role: "ideate", Purpose: purpose, Prompt: full, ReadOnly: true}}
	if err := run.buildContextPack(); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(scratch, "ideation-input-*.md"))
	if err != nil || len(files) != 1 {
		t.Fatalf("missing frozen input: %v", err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil || string(raw) != full {
		t.Fatal("mandatory instructions/history changed")
	}
	if len(run.input.Prompt) > 8192 || !strings.Contains(run.input.Prompt, filepath.Base(files[0])) {
		t.Fatal("bounded prompt lost frozen input reference")
	}
}
