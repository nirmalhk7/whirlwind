package workflow

import (
	"context"
	"reflect"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestUnmetReviewReturnsToCodingWithoutChangingApprovedRequirements(t *testing.T) {
	ctx := context.Background()
	e, task := localPlanFixture(t)
	view, err := e.ReadLocalPlan(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, version := localStoredTask(t, e, task.ID)
	if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""); err != nil {
		t.Fatal(err)
	}
	task, _ = localStoredTask(t, e, task.ID)
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	frozenRecord, err := e.Store.Get(ctx, "plans", task.ID+":1")
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := store.Decode[model.Plan](frozenRecord)
	if err != nil {
		t.Fatal(err)
	}
	approvedHash := task.PlanDocument.ApprovedHash
	task.Stage, task.Worktree, task.Base, task.ChecksPassed = "review", project.Path, submissionGit(t, project.Path, "rev-parse", "HEAD"), true
	judgment := model.RequirementResult{ID: "REQ-RESULT", Status: "fail", Explanation: "The result is still missing."}
	review := model.Review{Adherence: &model.Adherence{Score: 0, Passed: 0, Total: 1, Accepted: false, Results: []model.RequirementResult{judgment}}, Findings: []model.Finding{{ID: "missing-result", Message: "Implement the approved result behavior.", Blocking: true}}}
	step := &taskStep{Engine: e, ctx: ctx, project: project, task: task}
	if err := step.applyReview(review); err != nil {
		t.Fatal(err)
	}
	if step.task.Stage != "repair" || step.task.PlanVersion != 1 || step.task.PlanDocument.ApprovedHash != approvedHash || !reflect.DeepEqual(step.task.Plan.Requirements, frozen.Requirements) {
		t.Fatalf("review correction lost approved scope or waited for new approval: %+v", step.task)
	}
	if step.task.Delivery != nil || step.task.Submission != nil || step.task.PR != "" {
		t.Fatalf("unmet review finalized or published a candidate: %+v", step.task)
	}
}
