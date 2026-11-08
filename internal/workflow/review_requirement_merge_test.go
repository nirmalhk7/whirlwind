package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/testutil/openshell"
)

func TestSpecialistFailedRequirementOverridesPrimaryPass(t *testing.T) {
	openshell.Install(t)
	e, task, repo := testEngine(t)
	task.Worktree = repo
	task.PlanVersion, task.VerificationID, task.Risk = 1, "verification", "high"
	task.Plan = &model.Plan{Requirements: []model.Requirement{{ID: "REQ-1", Outcome: "reject unsafe input", EvidenceMethod: "run validation test"}}}
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	putFixtureAgent(t, e.Store, "review", model.AgentConfig{Role: "review", Chain: []model.AgentChoice{{Provider: "fake", Model: "fixture"}}})
	provenance := model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, VerificationID: task.VerificationID}
	if err := e.Store.Put(ctx, "verification", task.VerificationID, map[string]any{"provenance": provenance}, 0); err != nil {
		t.Fatal(err)
	}
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			judgment := model.RequirementResult{ID: "REQ-1", Status: "fail", Explanation: "The security review found an unsafe path."}
			response, err := json.Marshal(model.Review{Requirements: []model.RequirementResult{judgment}, Findings: []model.Finding{}, Summary: "unsafe path"})
			return provider.Output{Text: string(response), AttemptID: in.Purpose}, err
		})
	}
	commit, err := github.Run(ctx, repo, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	step := &taskStep{Engine: e, ctx: ctx, project: project, task: task}
	primary := model.Review{Requirements: []model.RequirementResult{{ID: "REQ-1", Status: "pass", Code: []string{"input.go:4"}, Evidence: []string{"TestRejectsUnsafeInput passed"}, Explanation: "The primary review found the validation."}}}
	if err := step.supplementReview("review", commit, &primary); err != nil {
		t.Fatal(err)
	}
	if len(primary.Requirements) != 1 || primary.Requirements[0].Status != "fail" {
		t.Fatalf("specialist failure was lost: requirements=%+v", primary.Requirements)
	}
}

func TestCriticalAcceptedLocalReviewWaitsForBoundHumanApproval(t *testing.T) {
	e, task, _ := testEngine(t)
	task.Risk = "critical"
	task.PlanVersion = 4
	task.ReviewID = "task-1:4:2"
	task.VerificationID = "verification-4"
	task.ReviewedCommit = "candidate-sha"
	task.ChecksPassed = true
	task.Plan = &model.Plan{Requirements: []model.Requirement{{ID: "R1", Outcome: "safe behavior", EvidenceMethod: "test"}}}
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	step := &taskStep{Engine: e, ctx: context.Background(), task: task, project: project}
	review := model.Review{Adherence: &model.Adherence{Score: 100, Passed: 1, Total: 1, Accepted: true, Results: []model.RequirementResult{{ID: "R1", Status: "pass"}}}}
	if err := step.applyReview(review); err != nil {
		t.Fatal(err)
	}
	if step.task.Stage != "approval" || step.task.Approved || step.task.Submission != nil {
		t.Fatalf("critical local work bypassed approval: %+v", step.task)
	}
	if len(step.pending) == 0 {
		t.Fatal("critical review did not produce an approval notification")
	}
	notification, ok := step.pending[0].Value.(model.Notification)
	if !ok {
		t.Fatalf("approval notification has unexpected type %T", step.pending[0].Value)
	}
	for _, expected := range []string{task.ReviewedCommit, task.ReviewID, task.VerificationID, project.PolicyHash, "plan version 4"} {
		if !strings.Contains(notification.Message, expected) {
			t.Fatalf("approval notification lacks binding %q: %q", expected, notification.Message)
		}
	}
}
