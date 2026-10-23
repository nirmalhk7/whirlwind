package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func localPlanFixture(t *testing.T) (*Engine, model.Task) {
	t.Helper()
	e, task, _ := testEngine(t)
	e.PlansRoot = filepath.Join(t.TempDir(), "plans")
	task.Mode = model.AgenticMode
	task.Plan = &model.Plan{Objective: "Improve result", What: "Change result.txt", Why: "Current result is incomplete", Benefit: "Result becomes useful", Where: []string{"result.txt"}, AcceptanceCriteria: []string{"result is correct"}, Units: []model.Unit{{ID: "main", Objective: "Write failing test, implement result, run checks", AllowedPaths: []string{"result.txt"}}}}
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.WriteLocalPlan(context.Background(), project, &task); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Store.Get(context.Background(), "tasks", task.ID)
	if err := e.Store.Put(context.Background(), "tasks", task.ID, task, r.Version); err != nil {
		t.Fatal(err)
	}
	return e, task
}

func localStoredTask(t *testing.T, e *Engine, id string) (model.Task, int) {
	t.Helper()
	r, err := e.Store.Get(context.Background(), "tasks", id)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Decode[model.Task](r)
	if err != nil {
		t.Fatal(err)
	}
	return task, r.Version
}

func TestLocalPlanApprovalBindsCurrentDocumentAndVersion(t *testing.T) {
	ctx := context.Background()
	e, task := localPlanFixture(t)
	view, err := e.ReadLocalPlan(ctx, task.ID)
	if err != nil || view.Hash == "" || view.Plan.Objective != "Improve result" {
		t.Fatalf("read plan: %+v %v", view, err)
	}
	_, version := localStoredTask(t, e, task.ID)
	if err := e.ReviewLocalPlan(ctx, task.ID, version-1, "approve", view.Hash, ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale version accepted: %v", err)
	}
	if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", "stale-hash", ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale hash accepted: %v", err)
	}
	if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""); err != nil {
		t.Fatal(err)
	}
	approved, _ := localStoredTask(t, e, task.ID)
	if approved.Stage != "coding" || approved.PlanDocument.ApprovedHash != view.Hash || approved.PlanDocument.ApprovedAt == "" {
		t.Fatalf("approval not recorded: %+v", approved)
	}
	if err := e.ValidateLocalPlan(ctx, approved); err != nil {
		t.Fatal(err)
	}
	snapshot, err := e.Store.Get(ctx, "plans", task.ID+":1")
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := store.Decode[model.Plan](snapshot)
	if plan.Objective != view.Plan.Objective {
		t.Fatal("approved snapshot not saved")
	}
	if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate approval accepted: %v", err)
	}
}

func TestLocalPlanEditRevokesApprovalAndRetainsImmutableSnapshot(t *testing.T) {
	ctx := context.Background()
	e, task := localPlanFixture(t)
	view, _ := e.ReadLocalPlan(ctx, task.ID)
	_, version := localStoredTask(t, e, task.ID)
	if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""); err != nil {
		t.Fatal(err)
	}
	approved, _ := localStoredTask(t, e, task.ID)
	stopped := false
	e.running[task.ID] = func() { stopped = true }
	if err := os.WriteFile(view.Path, []byte(strings.Replace(view.Content, "Improve result", "Improve revised result", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.ValidateLocalPlan(ctx, approved); err == nil {
		t.Fatal("edited approved document accepted")
	}
	changed, version := localStoredTask(t, e, task.ID)
	if changed.Stage != "plan_approval" || changed.PlanDocument.ApprovedHash != "" || changed.PlanVersion != 2 || !stopped {
		t.Fatalf("edited approval not revoked: %+v stopped=%v", changed, stopped)
	}
	updated, err := e.ReadLocalPlan(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", updated.Hash, ""); err != nil {
		t.Fatal(err)
	}
	old, _ := e.Store.Get(ctx, "plans", task.ID+":1")
	oldPlan, _ := store.Decode[model.Plan](old)
	if oldPlan.Objective != "Improve result" {
		t.Fatal("reapproval overwrote prior immutable snapshot")
	}
}

func TestLocalPlanRevisionAndRejection(t *testing.T) {
	for _, action := range []string{"revise", "reject"} {
		t.Run(action, func(t *testing.T) {
			e, task := localPlanFixture(t)
			_, version := localStoredTask(t, e, task.ID)
			if err := e.ReviewLocalPlan(context.Background(), task.ID, version, action, task.PlanDocument.Hash, "Cover retry behavior"); err != nil {
				t.Fatal(err)
			}
			changed, _ := localStoredTask(t, e, task.ID)
			if action == "reject" && changed.Stage != "cancelled" {
				t.Fatalf("reject did not cancel: %+v", changed)
			}
			if action == "revise" && (changed.Stage != "planning" || changed.PlanVersion != task.PlanVersion+1 || changed.PlanDocument != nil || !strings.Contains(changed.Issue.Body, "Cover retry behavior") || !strings.Contains(changed.Issue.Body, task.Issue.Body)) {
				t.Fatalf("revision lost objective or feedback: %+v", changed)
			}
		})
	}
}

func TestLocalPlanRejectsSymlinksAndArbitraryPaths(t *testing.T) {
	e, task := localPlanFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("private data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(task.PlanDocument.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, task.PlanDocument.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ReadLocalPlan(context.Background(), task.ID); err == nil {
		t.Fatal("symlink document read")
	}
	task.PlanDocument.Path = outside
	_, version := localStoredTask(t, e, task.ID)
	if err := e.Store.Put(context.Background(), "tasks", task.ID, task, version); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ReadLocalPlan(context.Background(), task.ID); err == nil {
		t.Fatal("arbitrary stored path read")
	}
}

func TestLocalPlanApprovalRejectsOutOfPolicyEdits(t *testing.T) {
	e, task := localPlanFixture(t)
	project, _ := e.Project(context.Background(), task.ProjectID)
	project.Policy.AllowedPaths = []string{"result.txt"}
	r, _ := e.Store.Get(context.Background(), "projects", project.ID)
	if err := e.Store.Put(context.Background(), "projects", project.ID, project, r.Version); err != nil {
		t.Fatal(err)
	}
	view, _ := e.ReadLocalPlan(context.Background(), task.ID)
	if err := os.WriteFile(view.Path, []byte(strings.ReplaceAll(view.Content, "`result.txt`", "`outside.txt`")), 0600); err != nil {
		t.Fatal(err)
	}
	edited, err := e.ReadLocalPlan(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, version := localStoredTask(t, e, task.ID)
	if err := e.ReviewLocalPlan(context.Background(), task.ID, version, "approve", edited.Hash, ""); err == nil {
		t.Fatal("out of policy edited scope approved")
	}
}

func TestLocalPlanEditRevokesApprovalWhenNextSnapshotCannotBeWritten(t *testing.T) {
	ctx := context.Background()
	e, task := localPlanFixture(t)
	view, _ := e.ReadLocalPlan(ctx, task.ID)
	_, version := localStoredTask(t, e, task.ID)
	if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""); err != nil {
		t.Fatal(err)
	}
	approved, _ := localStoredTask(t, e, task.ID)
	if err := os.WriteFile(view.Path, []byte(strings.Replace(view.Content, "Improve result", "Changed result", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(view.Path), "v2.md"), []byte("other retained edits"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.ValidateLocalPlan(ctx, approved); err == nil {
		t.Fatal("blocked edited snapshot accepted")
	}
	changed, _ := localStoredTask(t, e, task.ID)
	if changed.PlanDocument.ApprovedHash != "" || changed.Stage != "plan_approval" {
		t.Fatal("file write failure retained stale approval")
	}
}

func TestLocalPlanRetryRecoversPublishedDocumentBeforeTaskCommit(t *testing.T) {
	ctx := context.Background()
	e, task := localPlanFixture(t)
	path := task.PlanDocument.Path
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(content), "Improve result", "User retained objective", 1)
	if err := os.WriteFile(path, []byte(edited), 0600); err != nil {
		t.Fatal(err)
	}
	// The document reached disk, but the corresponding task transition did not.
	task.Stage, task.PlanDocument = "planning", nil
	task.Plan = nil
	_, version := localStoredTask(t, e, task.ID)
	if err := e.Store.Put(ctx, "tasks", task.ID, task, version); err != nil {
		t.Fatal(err)
	}
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	retry, version := localStoredTask(t, e, task.ID)
	retry.Plan = &model.Plan{Objective: "Different retry output", Why: "retry", Benefit: "retry benefit", Where: []string{"result.txt"}, AcceptanceCriteria: []string{"retry criteria"}, Units: []model.Unit{{ID: "main", Objective: "retry output", AllowedPaths: []string{"result.txt"}}}}
	if err := e.WriteLocalPlan(ctx, project, &retry); err != nil {
		t.Fatal(err)
	}
	if retry.Stage != "plan_approval" || retry.Plan.Objective != "User retained objective" || retry.PlanDocument.Path != path || retry.PlanDocument.Hash != localPlanHash(edited) || retry.PlanDocument.ApprovedHash != "" {
		t.Fatalf("orphan edits not recovered into unapproved task: %+v", retry)
	}
	if err := e.Store.Put(ctx, "tasks", retry.ID, retry, version); err != nil {
		t.Fatal(err)
	}
	view, err := e.ReadLocalPlan(ctx, retry.ID)
	if err != nil || view.Content != edited {
		t.Fatalf("recovered document changed: %+v %v", view, err)
	}
	_, version = localStoredTask(t, e, retry.ID)
	if err := e.ReviewLocalPlan(ctx, retry.ID, version, "approve", view.Hash, ""); err != nil {
		t.Fatal(err)
	}
}

func TestLocalPlanInvalidationRemainsApprovableAfterFailureHandler(t *testing.T) {
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
	approved, _ := localStoredTask(t, e, task.ID)
	if err := os.WriteFile(view.Path, []byte(strings.Replace(view.Content, "Improve result", "Edited result", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	err = e.ValidateLocalPlan(ctx, approved)
	if err == nil {
		t.Fatal("changed approval accepted")
	}
	e.fail(ctx, task.ID, err)
	current, version := localStoredTask(t, e, task.ID)
	if current.Stage != "plan_approval" {
		t.Fatalf("failure handler hid pending approval: %+v", current)
	}
	view, err = e.ReadLocalPlan(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""); err != nil {
		t.Fatalf("edited approval cannot recover: %v", err)
	}
}

func TestLocalPlanCycleDetectsEditsAndCancelsRunningTask(t *testing.T) {
	ctx := context.Background()
	e, task := localPlanFixture(t)
	defer e.background.Wait()
	view, err := e.ReadLocalPlan(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, version := localStoredTask(t, e, task.ID)
	if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""); err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	e.running[task.ID] = cancel
	if err := os.WriteFile(view.Path, []byte(strings.Replace(view.Content, "Improve result", "Edited live objective", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	e.runCycle(ctx, "", time.Now().Add(time.Hour))
	select {
	case <-run.Done():
	default:
		t.Fatal("edited plan left coding operation running")
	}
	current, _ := localStoredTask(t, e, task.ID)
	if current.Stage != "plan_approval" || current.PlanDocument.ApprovedHash != "" {
		t.Fatalf("cycle retained stale execution authorization: %+v", current)
	}
}

func TestLocalPlanStalePolicyValidationCannotReviveCancelledTask(t *testing.T) {
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
	stale, version := localStoredTask(t, e, task.ID)
	cancelled, _ := localStoredTask(t, e, task.ID)
	cancelled.Stage = "cancelled"
	if err := e.Store.Put(ctx, "tasks", task.ID, cancelled, version); err != nil {
		t.Fatal(err)
	}
	stale.PlanDocument.PolicyHash = "outdated-policy"
	if err := e.ValidateLocalPlan(ctx, stale); err == nil {
		t.Fatal("stale approval accepted")
	}
	current, _ := localStoredTask(t, e, task.ID)
	if current.Stage != "cancelled" {
		t.Fatalf("stale policy validation resurrected cancelled task: %+v", current)
	}
}
