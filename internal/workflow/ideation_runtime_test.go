package workflow

import (
	"context"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestIdeationGenerationSharesRecoveryAcrossNovelty(t *testing.T) {
	fixture := prepareNoveltyTest(t, []string{`{"unique":true}`})
	ctx := WithOperation(context.Background(), "generation-one")
	if err := fixture.engine.Ideate(ctx, fixture.project, "2026-10-21"); err != nil {
		t.Fatal(err)
	}
	records, err := fixture.engine.Store.List(ctx, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	var scope string
	for _, record := range records {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.Role != "ideate" {
			continue
		}
		if !strings.Contains(attempt.Task, "generation-one") {
			t.Fatalf("ideation lifetime scope used: %s", attempt.Task)
		}
		run := &agentRun{task: model.Task{ID: attempt.Task, PlanVersion: attempt.PlanVersion}}
		if scope != "" && scope != run.recoveryScope() {
			t.Fatal("novelty received independent recovery allowance")
		}
		scope = run.recoveryScope()
	}
	if scope == "" {
		t.Fatal("no ideation attempts captured")
	}
}
