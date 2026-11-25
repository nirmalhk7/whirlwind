package workflow

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	planEditFixtureMonthlyTitle = "# Improve monthly result"
	planEditFixtureTitle        = "# Improve result"
)

func TestParityPlanSavePublishesMarkdownWithoutApproval(t *testing.T) {
	e, task := localPlanFixture(t)
	ctx := context.Background()
	before, err := e.ReadLocalPlan(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, version := localStoredTask(t, e, task.ID)
	saver, ok := any(e).(interface {
		SaveLocalPlan(context.Context, string, int, string, string) error
	})
	if !ok {
		t.Fatal("supervisor cannot save actual edited Markdown")
	}
	edited := strings.Replace(before.Content, planEditFixtureTitle, planEditFixtureMonthlyTitle, 1)
	if err := saver.SaveLocalPlan(ctx, task.ID, version, before.Hash, edited); err != nil {
		t.Fatal(err)
	}
	after, err := e.ReadLocalPlan(ctx, task.ID)
	if err != nil || after.Content != edited || after.ApprovedHash != "" {
		t.Fatalf("saved content/approval mismatch: %+v %v", after, err)
	}
	current, newVersion := localStoredTask(t, e, task.ID)
	if current.Stage != "plan_approval" || current.PlanVersion != task.PlanVersion+1 || newVersion != version+1 {
		t.Fatalf("save ran or approved plan: %+v v%d", current, newVersion)
	}
	old, err := os.ReadFile(before.Path)
	if err != nil || string(old) != before.Content {
		t.Fatalf("save destroyed prior document: %v", err)
	}
	if err := saver.SaveLocalPlan(ctx, task.ID, version, before.Hash, edited); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale save accepted: %v", err)
	}
	if err := saver.SaveLocalPlan(ctx, task.ID, newVersion, "stale hash", edited); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("uninspected save accepted: %v", err)
	}
}

func TestParityActiveApprovedPlanWaitsBeforeSavingChanges(t *testing.T) {
	for _, stage := range []string{"coding", "review", "repair", "publishing", "submitting", "waiting_quota", "questions", "approval"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			e, task, view := approvePlanEditFixture(t, ctx)
			approved, version := localStoredTask(t, e, task.ID)
			approved.Stage = stage
			requireWorkflowSuccess(t, e.Store.Put(ctx, "execution_plans", task.ID, approved, version))
			active, version := localStoredTask(t, e, task.ID)
			stopped := false
			e.running[task.ID] = func() { stopped = true }
			edited := strings.Replace(view.Content, planEditFixtureTitle, planEditFixtureMonthlyTitle, 1)
			if err := e.SaveLocalPlan(ctx, task.ID, version, view.Hash, view.Content); err != nil {
				t.Fatalf("unchanged active plan save: %v", err)
			}
			current, currentVersion := localStoredTask(t, e, task.ID)
			if currentVersion != version || !reflect.DeepEqual(current, active) || stopped {
				t.Fatalf("unchanged active save changed plan or stopped runner: version=%d want=%d task=%+v stopped=%v", currentVersion, version, current, stopped)
			}

			err = e.SaveLocalPlan(ctx, task.ID, version, view.Hash, edited)
			if !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "wait until") || !strings.Contains(err.Error(), "pause") {
				t.Fatalf("active approved plan edit lacked actionable conflict: %v", err)
			}
			current, currentVersion = localStoredTask(t, e, task.ID)
			if currentVersion != version || !reflect.DeepEqual(current, active) || stopped {
				t.Fatalf("active approved plan changed or runner stopped: version=%d want=%d task=%+v stopped=%v", currentVersion, version, current, stopped)
			}
			content, err := os.ReadFile(view.Path)
			if err != nil || string(content) != view.Content {
				t.Fatalf("active document changed: %v", err)
			}
			assertFrozenPlanEditSnapshot(t, ctx, e, task.ID, view, "active immutable approval changed")
		})
	}
}

func TestParityPausedAndFinishedApprovedPlansCanBeEdited(t *testing.T) {
	for _, stage := range []string{"paused", "complete", "pr_created"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			e, task, view := approvePlanEditFixture(t, ctx)
			approved, version := localStoredTask(t, e, task.ID)
			setInactivePlanEditStage(t, ctx, e, approved, version, stage)
			version++
			edited := strings.Replace(view.Content, planEditFixtureTitle, planEditFixtureMonthlyTitle, 1)
			if err := e.SaveLocalPlan(ctx, task.ID, version, view.Hash, edited); err != nil {
				t.Fatalf("edit inactive approved plan: %v", err)
			}
			current, currentVersion := localStoredTask(t, e, task.ID)
			if currentVersion != version+1 || current.Stage != "plan_approval" || current.PlanVersion != task.PlanVersion+1 || current.PlanDocument.ApprovedHash != "" {
				t.Fatalf("inactive plan edit did not publish a new unapproved version: v%d %+v", currentVersion, current)
			}
			if err := e.ValidateLocalPlan(ctx, current); err == nil {
				t.Fatal("unapproved revision authorized execution")
			}
			assertRevisedPlanEditApproval(t, e, current.ID, currentVersion, view.Hash, edited)
			assertFrozenPlanEditSnapshot(t, ctx, e, task.ID, view, "prior approved snapshot changed")
		})
	}
}

func approvePlanEditFixture(t *testing.T, ctx context.Context) (*Engine, model.ExecutionPlan, PlanDocumentView) {
	t.Helper()
	e, task := localPlanFixture(t)
	view, err := e.ReadLocalPlan(ctx, task.ID)
	requireWorkflowSuccess(t, err)
	_, version := localStoredTask(t, e, task.ID)
	requireWorkflowSuccess(t, e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""))
	return e, task, view
}

func setInactivePlanEditStage(t *testing.T, ctx context.Context, e *Engine, approved model.ExecutionPlan, version int, stage string) {
	t.Helper()
	if stage == "paused" {
		requireWorkflowSuccess(t, e.SetPlanPaused(ctx, approved.ID, version, true))
		return
	}
	approved.Stage = stage
	requireWorkflowSuccess(t, e.Store.Put(ctx, "execution_plans", approved.ID, approved, version))
}

func assertRevisedPlanEditApproval(t *testing.T, e *Engine, taskID string, currentVersion int, oldHash, edited string) {
	t.Helper()
	ctx := context.Background()
	revised, err := e.ReadLocalPlan(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReviewLocalPlan(ctx, taskID, currentVersion, "approve", oldHash, ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("old document approved revision: %v", err)
	}
	if err := e.ReviewLocalPlan(ctx, taskID, currentVersion, "approve", revised.Hash, ""); err != nil {
		t.Fatal(err)
	}
	reapproved, _ := localStoredTask(t, e, taskID)
	if err := e.ValidateLocalPlan(ctx, reapproved); err != nil {
		t.Fatalf("new approval cannot execute: %v", err)
	}
	if reapproved.Plan.Specification != edited || reapproved.PlanDocument.ApprovedHash != revised.Hash {
		t.Fatal("new approval lost revised bytes")
	}
}

func assertFrozenPlanEditSnapshot(t *testing.T, ctx context.Context, e *Engine, id string, view PlanDocumentView, message string) {
	t.Helper()
	snapshot, err := e.Store.Get(ctx, "plan_versions", id+":1")
	requireWorkflowSuccess(t, err)
	frozen, err := store.Decode[model.PlanSpecification](snapshot)
	if err != nil || frozen.Specification != view.Content || frozen.Approval == nil || frozen.Approval.DocumentHash != view.Hash {
		t.Fatalf("%s: %+v %v", message, frozen, err)
	}
}

func TestParityApprovalFreezesExactDocumentBytes(t *testing.T) {
	e, plan := localPlanFixture(t)
	ctx := context.Background()
	view, err := e.ReadLocalPlan(ctx, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, version := localStoredTask(t, e, plan.ID)
	if err := e.ReviewLocalPlan(ctx, plan.ID, version, "approve", view.Hash, ""); err != nil {
		t.Fatal(err)
	}
	record, err := e.Store.Get(ctx, "plan_versions", plan.ID+":1")
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := store.Decode[model.PlanSpecification](record)
	if err != nil || frozen.Specification != view.Content {
		t.Fatalf("approved bytes changed: got %q want %q (%v)", frozen.Specification, view.Content, err)
	}
}

func TestParityPlanParserRetainsExactApprovedSourceBytes(t *testing.T) {
	e, plan := localPlanFixture(t)
	view, err := e.ReadLocalPlan(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePlanDocument(view.Content)
	if err != nil || parsed.Specification != view.Content {
		t.Fatal("parser changed approved source bytes", err)
	}
}

// Catches approval and edit commands committing over a competing decision.
func TestLocalPlanEditApprovalRaceRejectsStaleCommit(t *testing.T) {
	for _, first := range []string{"save", "approve"} {
		t.Run(first, func(t *testing.T) {
			assertLocalPlanEditApprovalRace(t, first)
		})
	}
}

func assertLocalPlanEditApprovalRace(t *testing.T, first string) {
	t.Helper()
	ctx := context.Background()
	e, task := localPlanFixture(t)
	view, err := e.ReadLocalPlan(ctx, task.ID)
	requireWorkflowSuccess(t, err)
	_, version := localStoredTask(t, e, task.ID)
	edited := strings.Replace(view.Content, localPlanObjective, "Revised result", 1)
	staged, batch := store.Stage(ctx)
	if first == "save" {
		err = e.SaveLocalPlan(staged, task.ID, version, view.Hash, edited)
	} else {
		err = e.ReviewLocalPlan(staged, task.ID, version, "approve", view.Hash, "")
	}
	requireWorkflowSuccess(t, err)
	if first == "save" {
		err = e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, "")
	} else {
		err = e.SaveLocalPlan(ctx, task.ID, version, view.Hash, edited)
	}
	requireWorkflowSuccess(t, err)
	winner, winnerVersion := localStoredTask(t, e, task.ID)
	if err := batch.Commit(staged, e.Store, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("competing decision committed: %v", err)
	}
	current, currentVersion := localStoredTask(t, e, task.ID)
	if currentVersion != winnerVersion || !reflect.DeepEqual(current, winner) {
		t.Fatal("losing decision changed winner")
	}
	if first == "approve" {
		if _, err := e.Store.Get(ctx, "plan_versions", task.ID+":1"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("losing approval published frozen version: %v", err)
		}
	}
}
