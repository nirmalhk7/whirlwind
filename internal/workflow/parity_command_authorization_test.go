package workflow

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestParityCanonicalPlanCannotDefineUnconfiguredVerificationCommands(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := context.Background()
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.dispatch(ctx)
	e.background.Wait()
	saved, err := e.Store.Get(ctx, "ideas", idea.ID)
	if err != nil {
		t.Fatal(err)
	}
	linked, _ := store.Decode[model.Idea](saved)
	record, err := e.Store.Get(ctx, "execution_plans", linked.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := e.ReadLocalPlan(ctx, linked.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	plan := view.Plan
	plan.RepositoryTestCommands = append(plan.RepositoryTestCommands, []string{"sh", "-c", "printf unapproved-command"})
	content := RenderPlanDocument(plan)
	if err := e.SaveLocalPlan(ctx, linked.PlanID, record.Version, view.Hash, content); err == nil || !strings.Contains(err.Error(), "verification configuration") {
		t.Fatalf("document save accepted unconfigured command: %v", err)
	}
	after, err := e.Store.Get(ctx, "execution_plans", linked.PlanID)
	if err != nil || after.Version != record.Version {
		t.Fatalf("failed command save changed resource: %v", err)
	}
	bytes, err := os.ReadFile(view.Path)
	if err != nil || string(bytes) != view.Content {
		t.Fatalf("failed command save changed document: %v", err)
	}
	// A manual filesystem edit cannot bypass the same approval boundary.
	if err := os.WriteFile(view.Path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.ReviewLocalPlan(ctx, linked.PlanID, record.Version, "approve", localPlanHash(content), ""); err == nil || !strings.Contains(err.Error(), "verification configuration") {
		t.Fatalf("approval accepted unconfigured command: %v", err)
	}
	versions, err := e.Store.List(ctx, "plan_versions")
	if err != nil || len(versions) != 0 {
		t.Fatalf("unconfigured command acquired immutable approval: %d %v", len(versions), err)
	}
}
