package workflow

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// Seed the candidate and review results, then exercise the real review
// transition, persistence, restart recovery, and execution lease fence.
func TestReviewCorrectionSurvivesReopenedSQLite(t *testing.T) {
	ctx := t.Context()
	engine, task := localPlanFixture(t)
	view, err := engine.ReadLocalPlan(ctx, task.ID)
	requireWorkflowSuccess(t, err)
	_, version := localStoredTask(t, engine, task.ID)
	requireWorkflowSuccess(t, engine.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""))
	task, version = localStoredTask(t, engine, task.ID)
	if task.Stage != "coding" {
		t.Fatalf("approval did not make coding runnable: %s", task.Stage)
	}
	project, err := engine.Project(ctx, task.ProjectID)
	requireWorkflowSuccess(t, err)
	task.Stage, task.Worktree = "review", project.Path
	task.Base = submissionGit(t, project.Path, "rev-parse", "HEAD")
	task.ReviewedCommit, task.VerificationID, task.ChecksPassed = task.Base, "fixture-verification", true
	proof := model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Head: task.Base, Base: task.Base, PolicyHash: project.PolicyHash, VerificationID: task.VerificationID}
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "verification", task.VerificationID, map[string]any{"provenance": proof}, 0))
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "execution_plans", task.ID, task, version))
	oldCtx, release, lease, err := engine.acquireStepLease(ctx, task.ID, project.ID)
	requireWorkflowSuccess(t, err)
	defer release()
	step, err := engine.prepareStep(oldCtx, task.ID)
	requireWorkflowSuccess(t, err)
	rejected := model.Review{Adherence: &model.Adherence{Total: 1, Results: []model.RequirementResult{{ID: "REQ-RESULT", Status: "fail"}}}, Findings: []model.Finding{{ID: "missing-result", Message: "Implement the approved behavior", Blocking: true}}}
	requireWorkflowSuccess(t, step.saveReview(rejected))
	requireWorkflowSuccess(t, step.applyReview(rejected))
	changes := append(step.pending, store.Change{Kind: "execution_plans", ID: task.ID, Value: step.task, Expected: step.record.Version})
	_, err = engine.Store.DB.Exec("CREATE TRIGGER reject_progress BEFORE INSERT ON resources WHEN NEW.kind='progress' BEGIN SELECT RAISE(ABORT,'fixture write failure'); END")
	requireWorkflowSuccess(t, err)
	if err := engine.Store.Write(oldCtx, changes, nil); err == nil {
		t.Fatal("correction committed despite failed progress write")
	}
	if _, err := engine.Store.Get(ctx, "reviews", step.task.ReviewID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("failed correction write exposed a partial review: %v", err)
	}
	if stored, _ := localStoredTask(t, engine, task.ID); stored.Stage != "review" {
		t.Fatalf("failed correction changed the persisted stage to %s", stored.Stage)
	}
	_, err = engine.Store.DB.Exec("DROP TRIGGER reject_progress")
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, engine.Store.Write(oldCtx, changes, nil))
	deadline := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	attempt := model.Attempt{AttemptID: "repair-before-restart", Task: task.ID, ProjectID: project.ID, Role: "code", Status: "running", Owner: engine.owner, DeadlineAt: deadline, PlanVersion: task.PlanVersion}
	requireWorkflowSuccess(t, engine.Store.Put(oldCtx, "sessions", attempt.AttemptID, attempt, 0))
	requireWorkflowSuccess(t, engine.Store.Close())
	resumedStore, err := store.Open(filepath.Join(engine.Root, "state.db"))
	requireWorkflowSuccess(t, err)
	defer func() { requireWorkflowSuccess(t, resumedStore.Close()) }()
	replacement := New(resumedStore, engine.GitHub, engine.Root)
	requireWorkflowSuccess(t, resumedStore.RecoverInterrupted(ctx, replacement.owner))
	resumed, currentVersion := localStoredTask(t, replacement, task.ID)
	if resumed.Stage != "repair" || resumed.ReviewID != step.task.ReviewID || resumed.PlanDocument.ApprovedHash != view.Hash || resumed.Progress == nil || resumed.Progress.BlockingFindings != 1 {
		t.Fatalf("replacement lost correction or approval: %+v", resumed)
	}
	work := RoleWork{ProjectID: resumed.ProjectID, Queue: CodingQueue, PlanID: resumed.ID, Version: currentVersion}
	requireWorkflowSuccess(t, replacement.PushRoleWork(ctx, work))
	requireWorkflowSuccess(t, replacement.PushRoleWork(ctx, work))
	for _, wrong := range []RoleWork{
		{ProjectID: resumed.ProjectID, Queue: ReviewerQueue, PlanID: resumed.ID, Version: currentVersion},
		{ProjectID: resumed.ProjectID, Queue: CodingQueue, PlanID: resumed.ID, Version: currentVersion - 1},
		{ProjectID: "other-project", Queue: CodingQueue, PlanID: resumed.ID, Version: currentVersion},
	} {
		if err := replacement.PushRoleWork(ctx, wrong); !errors.Is(err, store.ErrConflict) {
			t.Fatalf("accepted stale or misrouted push: %+v, %v", wrong, err)
		}
	}
	_, err = resumedStore.Get(ctx, "findings", task.ID+":1:missing-result")
	requireWorkflowSuccess(t, err)
	storedAttempt, err := resumedStore.Get(ctx, "sessions", attempt.AttemptID)
	requireWorkflowSuccess(t, err)
	interrupted, err := store.Decode[model.Attempt](storedAttempt)
	requireWorkflowSuccess(t, err)
	if interrupted.Status != "interrupted" || interrupted.DeadlineAt != deadline {
		t.Fatalf("replacement did not retain the interrupted attempt: %+v", interrupted)
	}
	_, releaseReplacement, next, err := replacement.acquireStepLease(ctx, task.ID, project.ID)
	requireWorkflowSuccess(t, err)
	defer releaseReplacement()
	if next.Fence <= lease.Fence {
		t.Fatalf("replacement did not fence the old worker: %d -> %d", lease.Fence, next.Fence)
	}
	resumed.Stage = "complete"
	if err := resumedStore.Put(context.WithoutCancel(oldCtx), "execution_plans", resumed.ID, resumed, currentVersion); !errors.Is(err, store.ErrLeased) {
		t.Fatalf("the former owner published after restart: %v", err)
	}
}
