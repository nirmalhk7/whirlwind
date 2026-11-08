package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestApproveLocalFinalizationBindsExactReviewAndCommitsReceipt(t *testing.T) {
	e, task, version, approval := criticalApprovalFixture(t)
	approved, err := e.ApproveLocalFinalization(context.Background(), task.ID, version, approval, "approve-critical-1")
	if err != nil {
		t.Fatal(err)
	}
	if approved.Stage != "submitting" || !approved.Approved || approved.Approval == nil {
		t.Fatalf("approval did not authorize local finalization: %+v", approved)
	}
	proof := approved.Approval
	if proof.Task != task.ID || proof.PlanVersion != approval.PlanVersion || proof.Head != approval.ReviewedCommit || proof.ReviewID != approval.ReviewID || proof.VerificationID != approval.VerificationID || proof.PolicyHash != approval.PolicyHash {
		t.Fatalf("approval proof differs from reviewed candidate: %+v", proof)
	}
	current, currentVersion := localStoredTask(t, e, task.ID)
	if currentVersion != version+1 || current.Stage != "submitting" {
		t.Fatalf("approval transition was not versioned: version=%d task=%+v", currentVersion, current)
	}
	audits, err := e.Store.List(context.Background(), "audit")
	if err != nil || !hasApprovalAudit(audits, task.ID, approval.ReviewID) {
		t.Fatalf("approval audit missing: audits=%+v err=%v", audits, err)
	}
	status, body, err := e.Store.Idempotent(context.Background(), "approve-critical-1", localFinalizationFingerprint(task.ID, version, approval))
	if err != nil || status != 200 {
		t.Fatalf("approval receipt missing: status=%d err=%v", status, err)
	}
	var receipted model.Task
	if err := json.Unmarshal(body, &receipted); err != nil || receipted.Stage != "submitting" {
		t.Fatalf("approval receipt has wrong result: task=%+v err=%v", receipted, err)
	}
	replayed, err := e.ApproveLocalFinalization(context.Background(), task.ID, version, approval, "approve-critical-1")
	if err != nil || replayed.Stage != "submitting" {
		t.Fatalf("idempotent retry did not replay after transition: task=%+v err=%v", replayed, err)
	}
	changed := approval
	changed.ReviewID += "-other"
	if _, err := e.ApproveLocalFinalization(context.Background(), task.ID, version, changed, "approve-critical-1"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("reused key with changed approval tuple was not rejected: %v", err)
	}
}

func TestApproveLocalFinalizationRejectsStaleTupleAndRollsBackReceiptFailure(t *testing.T) {
	e, task, version, approval := criticalApprovalFixture(t)
	stale := approval
	stale.ReviewedCommit = "another-candidate"
	if _, err := e.ApproveLocalFinalization(context.Background(), task.ID, version, stale, "approve-stale"); err == nil {
		t.Fatal("stale candidate approval was accepted")
	}
	if _, err := e.Store.DB.Exec("CREATE TRIGGER fail_finalization_receipt BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApproveLocalFinalization(context.Background(), task.ID, version, approval, "approve-trigger"); err == nil {
		t.Fatal("receipt failure did not abort approval")
	}
	unchanged, currentVersion := localStoredTask(t, e, task.ID)
	if currentVersion != version || unchanged.Stage != "approval" || unchanged.Approved || unchanged.Approval != nil {
		t.Fatalf("failed receipt left partial approval: version=%d task=%+v", currentVersion, unchanged)
	}
}

func criticalApprovalFixture(t *testing.T) (*Engine, model.Task, int, LocalFinalizationApproval) {
	t.Helper()
	ctx := context.Background()
	e, task := localPlanFixture(t)
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := github.Run(ctx, project.Path, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	task, version := localStoredTask(t, e, task.ID)
	task.Risk, task.Worktree, task.Base = "critical", project.Path, candidate
	if err := e.Store.Put(ctx, "tasks", task.ID, task, version); err != nil {
		t.Fatal(err)
	}
	task, version = localStoredTask(t, e, task.ID)
	view, err := e.ReadLocalPlan(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""); err != nil {
		t.Fatal(err)
	}
	task, version = localStoredTask(t, e, task.ID)
	task.ChecksPassed = true
	task.Stage = "approval"
	task.ReviewedCommit = candidate
	task.VerificationID = "verification-critical"
	task.ReviewID = task.ID + ":1:1"
	if err := e.Store.Put(ctx, "tasks", task.ID, task, version); err != nil {
		t.Fatal(err)
	}
	task, version = localStoredTask(t, e, task.ID)
	provenance := model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Head: candidate, Base: task.Base, PolicyHash: project.PolicyHash, VerificationID: task.VerificationID, ReviewID: task.ReviewID}
	verification := map[string]any{"stage": "running", "risk": task.Risk, "provenance": provenance}
	if err := e.Store.Put(ctx, "verification", task.VerificationID, verification, 0); err != nil {
		t.Fatal(err)
	}
	evidence := &acceptedReviewFixture{ctx: ctx, engine: e, task: task, project: project}
	verification["stage"], verification["report"] = "complete", evidence.completeReport(t)
	if err := e.Store.Put(ctx, "verification", task.VerificationID, verification, 1); err != nil {
		t.Fatal(err)
	}
	judgments := []model.RequirementResult{{ID: task.Plan.Requirements[0].ID, Status: "pass", Code: []string{"result.go:12"}, Evidence: []string{"required check passed"}, Explanation: "The approved outcome is present."}}
	adh := model.Adherence{Score: 100, Passed: 1, Total: 1, Accepted: true, Results: judgments, Provenance: provenance}
	if err := e.Store.Put(ctx, "reviews", task.ReviewID, model.Review{Requirements: judgments, Adherence: &adh, Provenance: &provenance}, 0); err != nil {
		t.Fatal(err)
	}
	approval := LocalFinalizationApproval{PlanVersion: task.PlanVersion, ApprovedPlanHash: task.PlanDocument.ApprovedHash, ReviewedCommit: candidate, ReviewID: task.ReviewID, VerificationID: task.VerificationID, PolicyHash: project.PolicyHash}
	return e, task, version, approval
}

func hasApprovalAudit(records []model.Record, taskID, reviewID string) bool {
	for _, record := range records {
		var audit model.Audit
		if json.Unmarshal(record.Data, &audit) == nil && audit.Task == taskID && audit.Action == "approve_local_finalization" && audit.Evidence != nil && audit.Evidence.ReviewID == reviewID {
			return true
		}
	}
	return false
}
