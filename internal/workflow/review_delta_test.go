package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
)

func TestRepairReviewUsesDeltaOnlyWithMatchingPriorEvidence(t *testing.T) {
	e, task, repo := testEngine(t)
	ctx := context.Background()
	git := func(args ...string) string {
		t.Helper()
		value, err := github.Run(ctx, repo, "git", args...)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	task.Worktree = repo
	task.Base = git("rev-parse", "HEAD")
	task.Risk = "low"
	task.Plan = &model.Plan{Objective: "fix", AcceptanceCriteria: []string{"correct"}}
	if err := os.WriteFile(filepath.Join(repo, "first.txt"), []byte("first change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "first")
	first := git("rev-parse", "HEAD")
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	prior := model.Review{Score: 7, Findings: []model.Finding{{ID: "remaining", Message: "fix remaining blocker", Blocking: true}}, Provenance: &model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Base: task.Base, Head: first, PolicyHash: project.PolicyHash, VerificationID: "v", ReviewID: "previous"}}
	if err = e.Store.Put(ctx, "reviews", task.ID+":1:1", prior, 0); err != nil {
		t.Fatal(err)
	}
	if err = e.Store.Put(ctx, "verification", "v", map[string]any{"stage": "complete", "risk": "low", "provenance": prior.Provenance}, 0); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(repo, "repair.txt"), []byte("repair change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "repair")
	task.VerificationID = "current"
	if err = e.Store.Put(ctx, "verification", task.VerificationID, map[string]any{"stage": "complete", "provenance": model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Base: task.Base, Head: git("rev-parse", "HEAD"), PolicyHash: project.PolicyHash}}, 0); err != nil {
		t.Fatal(err)
	}
	step := taskStep{Engine: e, ctx: ctx, project: project, task: task}
	snapshot, err := step.reviewSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot.Prompt, "Repair delta") || strings.Contains(snapshot.Prompt, "+first change") || !strings.Contains(snapshot.Prompt, "remaining blocker") {
		t.Fatalf("delta or findings lost: %s", snapshot.Prompt)
	}
	step.project.PolicyHash = "changed"
	snapshot, err = step.reviewSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot.Prompt, "+first change") {
		t.Fatal("changed policy incorrectly reused prior review context")
	}
}
