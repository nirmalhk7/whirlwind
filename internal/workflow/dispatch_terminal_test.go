package workflow

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"whirlwind/internal/store"
)

func TestStaleDispatchCannotReopenCompletedLocalDelivery(t *testing.T) {
	e, plan, project, record := acceptedDeliveryFixture(t)
	done := completeAcceptedLocalDeliveryFixture(t, e, plan, project, record)
	ctx := context.Background()
	before, err := e.Store.Get(ctx, "execution_plans", done.ID)
	requireWorkflowSuccess(t, err)
	// Historical completion must survive later setup changes. A dispatcher may
	// already have queued an older coding snapshot before completion committed.
	requireWorkflowSuccess(t, os.Remove(filepath.Join(project.Path, ".whirlwind.yaml")))
	stale := done
	stale.Stage = "coding"
	e.dispatchTask(ctx, stale, map[string]int{})
	e.background.Wait()
	after, err := e.Store.Get(ctx, "execution_plans", done.ID)
	requireWorkflowSuccess(t, err)
	if after.Version != before.Version || !bytes.Equal(after.Data, before.Data) {
		t.Fatal("stale dispatch changed a completed local delivery after policy changed")
	}
}

func TestStepPreparationRechecksCompletionAfterLeaseClaim(t *testing.T) {
	e, plan, project, record := acceptedDeliveryFixture(t)
	done := completeAcceptedLocalDeliveryFixture(t, e, plan, project, record)
	requireWorkflowSuccess(t, os.Remove(filepath.Join(project.Path, ".whirlwind.yaml")))
	ctx, release, _, err := e.acquireStepLease(context.Background(), done.ID, done.ProjectID)
	requireWorkflowSuccess(t, err)
	defer release()
	if _, err := e.prepareStep(ctx, done.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("completion after earlier eligibility read was not rejected before policy inspection: %v", err)
	}
}

func TestErrorWorkflowIsNotDispatchEligible(t *testing.T) {
	e, plan, _ := testEngine(t)
	ctx := context.Background()
	before, err := e.Store.Get(ctx, "execution_plans", plan.ID)
	requireWorkflowSuccess(t, err)
	plan.Stage = "error"
	requireWorkflowSuccess(t, e.Store.Put(ctx, "execution_plans", plan.ID, plan, before.Version))
	if _, ready, err := e.validateStepInput(ctx, plan.ID); ready || err != nil {
		t.Fatalf("terminal error workflow reached dispatch preflight: ready=%v error=%v", ready, err)
	}
}
