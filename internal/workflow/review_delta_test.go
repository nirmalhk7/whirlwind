package workflow

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
)

func TestSaveReviewStoresComputedPercentageAndOnlyPassedRequirementOutcomes(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	task.VerificationID = "verification-for-review"
	task.PlanVersion = 3
	task.ReviewedCommit = "candidate-head"
	task.Plan = &model.Plan{AcceptanceCriteria: []string{"legacy summary"}, Requirements: []model.Requirement{{ID: "R1", Outcome: "API behavior works", EvidenceMethod: "test"}, {ID: "R2", Outcome: "migration preserves history", EvidenceMethod: "test"}}}
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	proof := model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Head: task.ReviewedCommit, Base: task.Base, PolicyHash: project.PolicyHash, VerificationID: task.VerificationID, AttemptIDs: []string{"verify-attempt"}}
	if err := e.Store.Put(ctx, "verification", task.VerificationID, map[string]any{"provenance": proof}, 0); err != nil {
		t.Fatal(err)
	}
	task.ChecksPassed = true
	step := &taskStep{Engine: e, ctx: ctx, task: task, project: project}
	review := model.Review{Score: 0, Adherence: &model.Adherence{Score: 50, Passed: 1, Total: 2, Results: []model.RequirementResult{{ID: "R1", Status: "pass", Explanation: "pass"}, {ID: "R2", Status: "unverified", Explanation: "missing evidence"}}}}
	if err := step.saveReview(review); err != nil {
		t.Fatal(err)
	}
	savedReview, ok := step.pending[0].Value.(model.Review)
	if !ok || savedReview.Adherence == nil || savedReview.Provenance == nil || !reflect.DeepEqual(savedReview.Adherence.Provenance, *savedReview.Provenance) {
		t.Fatalf("computed adherence is not bound to saved review provenance: review=%+v", savedReview)
	}
	if step.task.Score != 50 {
		t.Fatalf("task score=%v, want computed 50%%", step.task.Score)
	}
	accepted := model.Review{Adherence: &model.Adherence{Score: 88.88888888888889, Passed: 8, Total: 9, Accepted: true, Results: []model.RequirementResult{{ID: "R1", Status: "pass", Explanation: "verified"}, {ID: "R2", Status: "unverified", Explanation: "not evidenced"}}}}
	step.task.Plan.Requirements = []model.Requirement{{ID: "R1", Outcome: "API behavior works", EvidenceMethod: "test"}, {ID: "R2", Outcome: "migration preserves history", EvidenceMethod: "test"}}
	if err := step.applyReview(accepted); err != nil {
		t.Fatal(err)
	}
	if strings.Join(step.task.Handoff.CompletedCriteria, ",") != "R1: API behavior works" {
		t.Fatalf("unverified outcome marked complete: %v", step.task.Handoff.CompletedCriteria)
	}
	if !strings.Contains(step.task.Handoff.NextAction, "local") {
		t.Fatalf("accepted review routed to a remote publication handoff: %q", step.task.Handoff.NextAction)
	}
}

const (
	reviewDeltaGitRevParse = "rev-parse"
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
	task.Base = git(reviewDeltaGitRevParse, "HEAD")
	task.Risk = "low"
	task.Plan = &model.Plan{Objective: "fix", AcceptanceCriteria: []string{"correct"}}
	if err := os.WriteFile(filepath.Join(repo, "first.txt"), []byte("first change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "first")
	first := git(reviewDeltaGitRevParse, "HEAD")
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
	if err = e.Store.Put(ctx, "verification", task.VerificationID, map[string]any{"stage": "complete", "provenance": model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Base: task.Base, Head: git(reviewDeltaGitRevParse, "HEAD"), PolicyHash: project.PolicyHash}}, 0); err != nil {
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
