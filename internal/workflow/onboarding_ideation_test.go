package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func queuedOnboardingIdeation(t *testing.T, e *Engine, project model.Project, id string, input map[string]any) {
	t.Helper()
	requireWorkflowSuccess(t, e.Store.Put(t.Context(), "operations", id, model.Operation{
		Kind: "ideation", Subject: project.ID, Stage: "queued", Input: input,
	}, 0))
}

func inspectOnboardingIdeation(t *testing.T, e *Engine, id string) (model.Operation, int) {
	t.Helper()
	record, err := e.Store.Get(t.Context(), "operations", id)
	requireWorkflowSuccess(t, err)
	op, err := store.Decode[model.Operation](record)
	requireWorkflowSuccess(t, err)
	return op, record.Version
}

func setOnboardingProjectStatus(t *testing.T, e *Engine, project model.Project, status string) {
	t.Helper()
	record, err := e.Store.Get(t.Context(), "projects", project.ID)
	requireWorkflowSuccess(t, err)
	project.Status = status
	requireWorkflowSuccess(t, e.Store.Put(t.Context(), "projects", project.ID, project, record.Version))
}

func TestRegistrationIdeationDoesNotStartUntilSetupSucceeds(t *testing.T) {
	f := prepareNoveltyTest(t, nil)
	e := f.engine
	setOnboardingProjectStatus(t, e, f.project, "")
	queuedOnboardingIdeation(t, e, f.project, "automatic", map[string]any{
		"project_id": f.project.ID, "day": "2026-11-24", "trigger": "project_registration",
	})
	check := func(reason string, generations int) int {
		t.Helper()
		e.dispatchQueuedIdeation(t.Context())
		e.background.Wait()
		op, version := inspectOnboardingIdeation(t, e, "automatic")
		if op.Stage != "queued" || !strings.Contains(op.Error, reason) || f.fixture.generations != generations {
			t.Fatalf("automatic ideation eligibility was not enforced: op=%+v generations=%d", op, f.fixture.generations)
		}
		return version
	}

	waiting := check("waiting for project setup", 0)
	setOnboardingProjectStatus(t, e, f.project, "draft")
	if again := check("waiting for project setup", 0); again != waiting {
		t.Fatalf("checking unchanged hold wrote a duplicate update: %d → %d", waiting, again)
	}
	setOnboardingProjectStatus(t, e, f.project, "error")
	check("project setup failed", 0)
	setOnboardingProjectStatus(t, e, f.project, "approved")
	for range 2 {
		e.dispatchQueuedIdeation(t.Context())
		e.background.Wait()
	}
	op, _ := inspectOnboardingIdeation(t, e, "automatic")
	if op.Stage != "complete" || op.Error != "" || f.fixture.generations != 1 {
		t.Fatalf("ready project did not generate ideas exactly once: op=%+v generations=%d", op, f.fixture.generations)
	}
}

func TestLegacyRegistrationOptOutDoesNotBlockManualIdeation(t *testing.T) {
	f := prepareNoveltyTest(t, nil)
	e := f.engine
	setOnboardingProjectStatus(t, e, f.project, "approved")
	settings, err := e.Store.Get(t.Context(), "settings", "global")
	requireWorkflowSuccess(t, err)
	var workers map[string]any
	requireWorkflowSuccess(t, json.Unmarshal(settings.Data, &workers))
	workers["ideation"].(map[string]any)["on_registration"] = false
	requireWorkflowSuccess(t, e.Store.Put(t.Context(), "settings", "global", workers, settings.Version))
	queuedOnboardingIdeation(t, e, f.project, "legacy-automatic", map[string]any{
		"project_id": f.project.ID, "day": "2026-11-24",
	})
	e.dispatchQueuedIdeation(t.Context())
	e.background.Wait()
	op, _ := inspectOnboardingIdeation(t, e, "legacy-automatic")
	if op.Stage != "queued" || !strings.Contains(op.Error, "on_registration") || f.fixture.generations != 0 {
		t.Fatalf("disabled legacy onboarding reached an external model: op=%+v generations=%d", op, f.fixture.generations)
	}
	setOnboardingProjectStatus(t, e, f.project, "error")
	queuedOnboardingIdeation(t, e, f.project, "manual", map[string]any{
		"project_id": f.project.ID, "day": "2026-11-24", "fresh": false,
	})
	e.dispatchQueuedIdeation(t.Context())
	e.background.Wait()
	manual, _ := inspectOnboardingIdeation(t, e, "manual")
	if manual.Stage != "complete" || f.fixture.generations != 1 {
		t.Fatalf("manual ideation was unexpectedly held: op=%+v generations=%d", manual, f.fixture.generations)
	}
}

func TestRegistrationIdeationRechecksSetupAtLeasedStart(t *testing.T) {
	f := prepareNoveltyTest(t, nil)
	e := f.engine
	staleReady := f.project
	staleReady.Status = "approved"
	setOnboardingProjectStatus(t, e, f.project, "error")
	queuedOnboardingIdeation(t, e, f.project, "waiting", map[string]any{
		"project_id": f.project.ID, "day": "2026-11-24", "trigger": "project_registration",
	})
	record, err := e.Store.Get(t.Context(), "operations", "waiting")
	requireWorkflowSuccess(t, err)
	lease, err := e.Store.Claim(t.Context(), "operation:waiting", e.owner, time.Minute)
	requireWorkflowSuccess(t, err)
	defer func() { _ = e.Store.Release(context.Background(), "operation:waiting", e.owner, lease.Fence) }()
	requireWorkflowSuccess(t, e.runQueuedIdeation(t.Context(), staleReady, record, "operation:waiting", lease.Fence))
	op, _ := inspectOnboardingIdeation(t, e, "waiting")
	if op.Stage != "queued" || !strings.Contains(op.Error, "project setup failed") || f.fixture.generations != 0 {
		t.Fatalf("stale project readiness granted an automatic model attempt: %+v", op)
	}
}

func TestRegistrationIdeationGuardsPreviouslyMissingSettings(t *testing.T) {
	f := prepareNoveltyTest(t, nil)
	ctx, e := t.Context(), f.engine
	setOnboardingProjectStatus(t, e, f.project, "approved")
	_, err := e.Store.DB.ExecContext(ctx, "DELETE FROM resources WHERE kind='settings' AND id='global'")
	requireWorkflowSuccess(t, err)
	admission, err := e.onboardingIdeationAdmission(ctx, f.project.ID)
	requireWorkflowSuccess(t, err)
	if admission.hold != "" {
		t.Fatalf("default admission did not reach the lease transition: %s", admission.hold)
	}
	want := false
	settings := DefaultSettings()
	settings.Ideation.OnRegistration = &want
	requireWorkflowSuccess(t, e.Store.Put(ctx, "settings", "global", settings, 0))
	op := model.Operation{Kind: "ideation", Subject: f.project.ID, Stage: "running"}
	changes := append(admission.guards, store.Change{Kind: "operations", ID: "stale-admission", Value: op})
	if err := e.Store.Write(store.WithoutSettingsSource(ctx), changes, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("creation of opt-out settings did not invalidate the earlier default: %v", err)
	}
	if _, err := e.Store.Get(ctx, "operations", "stale-admission"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stale admission persisted a model run: %v", err)
	}
}
