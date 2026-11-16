package workflow

import (
	"context"
	"errors"
	"os"
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
