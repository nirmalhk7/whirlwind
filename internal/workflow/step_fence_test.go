package workflow

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// Losing task ownership must reject publication before heartbeat cancellation,
// even when the resource version itself has not changed.
func TestPlanStepLeaseFencesStaleWorkflowWrites(t *testing.T) {
	for _, tc := range []struct{ name, change string }{
		{"owner changed", "owner='replacement'"},
		{"generation changed", "fence=fence+1"},
		{"lease expired", "expires=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, plan, _ := testEngine(t)
			ctx, release, lease, err := e.acquireStepLease(context.Background(), plan.ID, plan.ProjectID)
			requireWorkflowSuccess(t, err)
			defer release()
			initial, err := e.Store.Get(ctx, "execution_plans", plan.ID)
			requireWorkflowSuccess(t, err)
			plan.Error = "live worker checkpoint"
			requireWorkflowSuccess(t, e.Store.Put(ctx, "execution_plans", plan.ID, plan, initial.Version))
			before, err := e.Store.Get(ctx, "execution_plans", plan.ID)
			requireWorkflowSuccess(t, err)
			var events int
			requireWorkflowSuccess(t, e.Store.DB.QueryRow("SELECT count(*) FROM events").Scan(&events))
			result, err := e.Store.DB.Exec("UPDATE leases SET "+tc.change+" WHERE id=? AND owner=? AND fence=?", "task:"+plan.ID, e.owner, lease.Fence)
			requireWorkflowSuccess(t, err)
			count, err := result.RowsAffected()
			requireWorkflowSuccess(t, err)
			if count != 1 {
				t.Fatalf("failed to revoke original task lease: %d rows", count)
			}
			plan.Error = "stale worker completion"
			err = e.Store.Write(context.WithoutCancel(ctx), []store.Change{
				{Kind: "execution_plans", ID: plan.ID, Value: plan, Expected: before.Version},
				{Kind: "step_fence_probe", ID: plan.ID, Value: "stale completion"},
			}, nil)
			if !errors.Is(err, store.ErrLeased) {
				t.Fatalf("stale worker publication accepted before heartbeat cancellation: %v", err)
			}
			assertStaleStepWriteRolledBack(t, e, before, events)
		})
	}
}

func assertStaleStepWriteRolledBack(t *testing.T, e *Engine, before model.Record, events int) {
	t.Helper()
	after, err := e.Store.Get(context.Background(), "execution_plans", before.ID)
	requireWorkflowSuccess(t, err)
	if after.Version != before.Version || !bytes.Equal(after.Data, before.Data) {
		t.Fatal("stale completion changed durable workflow state")
	}
	if _, err := e.Store.Get(context.Background(), "step_fence_probe", before.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stale completion published a companion record: %v", err)
	}
	var afterEvents int
	requireWorkflowSuccess(t, e.Store.DB.QueryRow("SELECT count(*) FROM events").Scan(&afterEvents))
	if afterEvents != events {
		t.Fatalf("stale completion emitted events: %d became %d", events, afterEvents)
	}
}

func TestLostStepLeaseDoesNotBecomeWorkflowFailure(t *testing.T) {
	e, plan, path := testEngine(t)
	ctx, release, _, err := e.acquireStepLease(context.Background(), plan.ID, plan.ProjectID)
	requireWorkflowSuccess(t, err)
	defer release()
	project, err := e.Project(ctx, plan.ProjectID)
	requireWorkflowSuccess(t, err)
	attempt := newLeaseTestAttempt(e, plan, project, path, "lost-step-owner")
	attempt.ctx = ctx
	requireWorkflowSuccess(t, attempt.launch())
	_, err = e.Store.DB.Exec("UPDATE leases SET expires=0 WHERE id=?", "task:"+plan.ID)
	requireWorkflowSuccess(t, err)
	verification := verificationRun{engine: e, ctx: ctx, task: &plan, id: "lost-step-verification", value: map[string]any{"stage": "complete"}}
	attempt.event("message", map[string]any{"text": "late output"})
	rejected := e.Store.Put(ctx, "step_fence_probe", plan.ID, "late output", 0)
	for name, failure := range map[string]error{
		"verification": verification.save(),
		"agent event":  attempt.eventErr,
		"agent write":  attempt.persistenceError(rejected),
	} {
		if !errors.Is(failure, store.ErrPersistence) || !errors.Is(failure, store.ErrLeased) || stepFailed(failure) {
			t.Errorf("%s lost ownership cause or would pause replacement workflow: %v", name, failure)
		}
	}
}
