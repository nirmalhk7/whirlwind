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
	edited := strings.Replace(before.Content, "# Improve result", "# Improve monthly result", 1)
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
	for _, stage := range []string{"coding", "review", "repair", "publishing", "submitting", "waiting_quota"} {
		t.Run(stage, func(t *testing.T) {
			e, task := localPlanFixture(t)
			ctx := context.Background()
			view, err := e.ReadLocalPlan(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, version := localStoredTask(t, e, task.ID)
			if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""); err != nil {
				t.Fatal(err)
			}
			approved, version := localStoredTask(t, e, task.ID)
			approved.Stage = stage
			if err := e.Store.Put(ctx, "execution_plans", task.ID, approved, version); err != nil {
				t.Fatal(err)
			}
			active, version := localStoredTask(t, e, task.ID)
			stopped := false
			e.running[task.ID] = func() { stopped = true }
			edited := strings.Replace(view.Content, "# Improve result", "# Improve monthly result", 1)
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
			snapshot, err := e.Store.Get(ctx, "plan_versions", task.ID+":1")
			if err != nil {
				t.Fatal(err)
			}
			frozen, err := store.Decode[model.PlanSpecification](snapshot)
			if err != nil || frozen.Specification != view.Content || frozen.Approval == nil || frozen.Approval.DocumentHash != view.Hash {
				t.Fatalf("active immutable approval changed: %+v %v", frozen, err)
			}
		})
	}
}

func TestParityPausedAndFinishedApprovedPlansCanBeEdited(t *testing.T) {
	for _, stage := range []string{"paused", "complete", "pr_created"} {
		t.Run(stage, func(t *testing.T) {
			e, task := localPlanFixture(t)
			ctx := context.Background()
			view, err := e.ReadLocalPlan(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, version := localStoredTask(t, e, task.ID)
			if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""); err != nil {
				t.Fatal(err)
			}
			approved, version := localStoredTask(t, e, task.ID)
			approved.Stage = stage
			if err := e.Store.Put(ctx, "execution_plans", task.ID, approved, version); err != nil {
				t.Fatal(err)
			}
			version++
			edited := strings.Replace(view.Content, "# Improve result", "# Improve monthly result", 1)
			if err := e.SaveLocalPlan(ctx, task.ID, version, view.Hash, edited); err != nil {
				t.Fatalf("edit inactive approved plan: %v", err)
			}
			current, currentVersion := localStoredTask(t, e, task.ID)
			if currentVersion != version+1 || current.Stage != "plan_approval" || current.PlanVersion != task.PlanVersion+1 || current.PlanDocument.ApprovedHash != "" {
				t.Fatalf("inactive plan edit did not publish a new unapproved version: v%d %+v", currentVersion, current)
			}
			snapshot, err := e.Store.Get(ctx, "plan_versions", task.ID+":1")
			if err != nil {
				t.Fatal(err)
			}
			frozen, err := store.Decode[model.PlanSpecification](snapshot)
			if err != nil || frozen.Specification != view.Content || frozen.Approval == nil || frozen.Approval.DocumentHash != view.Hash {
				t.Fatalf("prior approved snapshot changed: %+v %v", frozen, err)
			}
		})
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
