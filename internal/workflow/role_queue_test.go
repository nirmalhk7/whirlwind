package workflow

import (
	"errors"
	"testing"

	"whirlwind/internal/store"
)

func TestRoleQueuePopUsesFencedPlannerStep(t *testing.T) {
	ctx := t.Context()
	engine, task := localPlanFixture(t)
	task, version := localStoredTask(t, engine, task.ID)
	work := RoleWork{ProjectID: task.ProjectID, Queue: PlannerQueue, PlanID: task.ID, Version: version}
	if err := engine.PushRoleWork(ctx, work); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("unapproved task appeared in Planner queue: %v", err)
	}
	task.Stage = "plan_ready"
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "execution_plans", task.ID, task, version))
	work.Version++
	requireWorkflowSuccess(t, engine.PushRoleWork(ctx, work))
	result := engine.popRoleWork(ctx, rolePop{work: work, caller: ctx})
	if result.err != nil || !result.scheduled {
		t.Fatalf("pop did not admit Planner publication: %+v", result)
	}
	engine.background.Wait()
	current, version := localStoredTask(t, engine, task.ID)
	if version != work.Version+1 || current.Stage != "plan_approval" {
		t.Fatalf("pop missed normal approval handoff: version=%d stage=%s", version, current.Stage)
	}
	if _, err := engine.PopRoleWork(ctx, work); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate pop ran a completed revision: %v", err)
	}
}
