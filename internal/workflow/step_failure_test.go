package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/store"
)

func TestDispatchedFailureCannotPauseAfterLeaseHandoff(t *testing.T) {
	e, plan, path := testEngine(t)
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	// The SQLite trigger claims the replacement exactly when Step releases its
	// lease, recording the workflow version that replacement actually observes.
	_, err := e.Store.DB.Exec(fmt.Sprintf(`CREATE TRIGGER claim_replacement AFTER UPDATE OF expires ON leases
 WHEN NEW.id=%s AND NEW.expires=0
 BEGIN
 INSERT INTO resources(kind,id,version,data,updated_at)
 SELECT 'step_handoff_probe',id,1,json_object('plan_version',version),updated_at FROM resources WHERE kind='execution_plans' AND id=%s;
 UPDATE leases SET owner='replacement',fence=fence+1,expires=unixepoch('subsec')*1000+60000 WHERE id=NEW.id;
 END`, quote("task:"+plan.ID), quote(plan.ID)))
	requireWorkflowSuccess(t, err)
	// Fail setup after acquiring the step lease, before any ACP process starts.
	requireWorkflowSuccess(t, os.Remove(filepath.Join(path, ".whirlwind.yaml")))
	e.dispatch(context.Background())
	e.background.Wait()
	handoff, err := e.Store.Get(context.Background(), "step_handoff_probe", plan.ID)
	requireWorkflowSuccess(t, err)
	observed, err := store.Decode[struct {
		Version int `json:"plan_version"`
	}](handoff)
	requireWorkflowSuccess(t, err)
	current, err := e.Store.Get(context.Background(), "execution_plans", plan.ID)
	requireWorkflowSuccess(t, err)
	if current.Version != observed.Version {
		t.Fatalf("old worker changed replacement workflow after handoff: replacement saw version %d, current version %d", observed.Version, current.Version)
	}
	notices, err := e.Store.List(context.Background(), "inbox")
	requireWorkflowSuccess(t, err)
	if len(recoveryNotifications(notices)) != 1 {
		t.Fatal("owned failure did not commit exactly one recovery notice before handoff")
	}
}

func TestOwnedStepFailureCannotPauseChangedPlanVersion(t *testing.T) {
	e, plan, _ := testEngine(t)
	ctx, release, _, err := e.acquireStepLease(context.Background(), plan.ID, plan.ProjectID)
	requireWorkflowSuccess(t, err)
	defer release()
	step, err := e.prepareStep(ctx, plan.ID)
	requireWorkflowSuccess(t, err)
	plan.PlanVersion++
	plan.Error = "new human-approved workflow version"
	requireWorkflowSuccess(t, e.Store.Put(context.Background(), "execution_plans", plan.ID, plan, step.record.Version))
	before, err := e.Store.Get(context.Background(), "execution_plans", plan.ID)
	requireWorkflowSuccess(t, err)
	e.failOwnedStep(step, errors.New("previous version failed"))
	after, err := e.Store.Get(context.Background(), "execution_plans", plan.ID)
	requireWorkflowSuccess(t, err)
	if after.Version != before.Version || !bytes.Equal(after.Data, before.Data) {
		t.Fatal("old owned failure changed a newer workflow version")
	}
	notices, err := e.Store.List(context.Background(), "inbox")
	requireWorkflowSuccess(t, err)
	if len(recoveryNotifications(notices)) != 0 {
		t.Fatal("old owned failure emitted a recovery notice for the newer version")
	}
}
