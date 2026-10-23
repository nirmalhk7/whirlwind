package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"whirlwind/internal/model"
)

// Rewriting code without improving failing checks must not reset the stall counter.
func TestChangedDiffDoesNotHideUnchangedFailureEvidence(t *testing.T) {
	e, task, repo := testEngine(t)
	task.Worktree = repo
	task.Base = "HEAD"
	task.Stage = "repair"
	task.Handoff.FailingTests = []string{"unit: still failing"}
	path := filepath.Join(repo, ".whirlwind.yaml")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		if err := os.WriteFile(path, append(append([]byte{}, original...), []byte("# churn "+string(rune('a'+i)))...), 0600); err != nil {
			t.Fatal(err)
		}
		if err := e.progress(context.Background(), &task); err != nil {
			t.Fatal(err)
		}
	}
	if task.Stage != "paused" || task.Recoveries != 3 {
		t.Fatalf("diff churn hid stagnation: %+v", task)
	}
}

// No earlier review is not evidence of zero blockers; a later resolved blocker is progress.
func TestResolvedReviewBlockerResetsStagnationAfterInitialPlanning(t *testing.T) {
	e, task, repo := testEngine(t)
	ctx := context.Background()
	task.Worktree = repo
	task.Base = "HEAD"
	task.Stage = "repair"
	if err := e.progress(ctx, &task); err != nil {
		t.Fatal(err)
	}
	review := model.Review{Findings: []model.Finding{{ID: "a", Blocking: true}, {ID: "b", Blocking: true}}}
	if err := e.Store.Put(ctx, "reviews", "r", review, 0); err != nil {
		t.Fatal(err)
	}
	task.ReviewID = "r"
	if err := e.progress(ctx, &task); err != nil {
		t.Fatal(err)
	}
	review.Findings[0].Resolved = true
	if err := e.Store.Put(ctx, "reviews", "r", review, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.progress(ctx, &task); err != nil {
		t.Fatal(err)
	}
	if task.UnchangedFailures != 0 {
		t.Fatalf("resolved blocker ignored after initial unknown review: %+v", task.Progress)
	}
}
