package workflow

import (
	"context"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestParityCanonicalExecutionNeedsNoWholePlanBudget(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := context.Background()
	e.intakeApprovedIdeas(ctx)
	r, err := e.Store.Get(ctx, "execution_plans", model.PlanID("idea:"+idea.ID))
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := store.Decode[model.ExecutionPlan](r)
	project, err := e.Project(ctx, idea.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settingsRecord, _ := e.Store.Get(ctx, "settings", "global")
	settings := e.settings(ctx)
	settings.Admission.DefaultBudget = nil
	if err := e.Store.Put(ctx, "settings", "global", settings, settingsRecord.Version); err != nil {
		t.Fatal(err)
	}
	run := agentRun{Engine: e, ctx: ctx, project: project, task: plan}
	if _, err := run.taskBudgetPolicy(); err != nil {
		t.Fatal("canonical execution required whole-plan ceilings", err)
	}
	project.Policy.RunTimeoutSeconds = 1
	checkCtx, finish, err := e.verificationBudget(ctx, project, plan)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-checkCtx.Done():
		t.Fatal("aggregate verification deadline replaced per-command deadlines")
	case <-time.After(1100 * time.Millisecond):
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	usage, err := e.Store.TaskUsage(ctx, plan.ID)
	if err != nil || usage.ElapsedMS < 1000 {
		t.Fatal("verification usage not retained", usage, err)
	}
}
