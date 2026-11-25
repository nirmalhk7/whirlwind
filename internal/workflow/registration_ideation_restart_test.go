package workflow

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestRegistrationOptOutSurvivesGenerationLeaseRetry(t *testing.T) {
	f := prepareNoveltyTest(t, nil)
	ctx, engine := t.Context(), f.engine
	project := f.project
	project.Status = "approved"
	record, err := engine.Store.Get(ctx, "projects", project.ID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "projects", project.ID, project, record.Version))
	const id = "legacy-registration-retry"
	op := model.Operation{Kind: "ideation", Subject: project.ID, Stage: "queued", Input: map[string]string{"project_id": project.ID, "day": "2026-11-24"}}
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "operations", id, op, 0))
	competingOwner := "manual-generation-lease"
	lease, err := engine.Store.Claim(ctx, "generation:"+project.ID, competingOwner, time.Minute)
	requireWorkflowSuccess(t, err)
	engine.dispatchQueuedIdeation(ctx)
	engine.background.Wait()
	record, err = engine.Store.Get(ctx, "operations", id)
	requireWorkflowSuccess(t, err)
	retry, err := store.Decode[model.Operation](record)
	requireWorkflowSuccess(t, err)
	if retry.Stage != "queued" || !retry.RegistrationIdeation() || f.fixture.generations != 0 {
		t.Fatalf("fixture did not reproduce a lease-blocked retry: stage=%s generations=%d", retry.Stage, f.fixture.generations)
	}
	settings, err := engine.Store.Get(ctx, "settings", "global")
	requireWorkflowSuccess(t, err)
	var workers map[string]any
	requireWorkflowSuccess(t, json.Unmarshal(settings.Data, &workers))
	workers["ideation"].(map[string]any)["on_registration"] = false
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "settings", "global", workers, settings.Version))
	requireWorkflowSuccess(t, engine.Store.Release(ctx, "generation:"+project.ID, competingOwner, lease.Fence))
	engine.dispatchQueuedIdeation(ctx)
	engine.background.Wait()
	record, err = engine.Store.Get(ctx, "operations", id)
	requireWorkflowSuccess(t, err)
	op, err = store.Decode[model.Operation](record)
	requireWorkflowSuccess(t, err)
	if op.Stage != "queued" || !strings.Contains(op.Error, "on_registration") || f.fixture.generations != 0 {
		t.Fatalf("disabled automatic ideation ran after lease retry: previousOwner=%q finalStage=%s generations=%d error=%q", retry.Owner, op.Stage, f.fixture.generations, op.Error)
	}
}

func TestLegacyRegistrationOwnerDoesNotEraseAutomaticTrigger(t *testing.T) {
	for _, tc := range []struct {
		name, originalOwner string
		wantAutomatic       bool
	}{
		{name: "automatic began without an owner", wantAutomatic: true},
		{name: "legacy manual queue had an owner", originalOwner: "operator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := prepareNoveltyTest(t, nil)
			ctx, engine := t.Context(), f.engine
			setOnboardingProjectStatus(t, engine, f.project, "approved")
			settings, err := engine.Store.Get(ctx, "settings", "global")
			requireWorkflowSuccess(t, err)
			var workers map[string]any
			requireWorkflowSuccess(t, json.Unmarshal(settings.Data, &workers))
			workers["ideation"].(map[string]any)["on_registration"] = false
			requireWorkflowSuccess(t, engine.Store.Put(ctx, "settings", "global", workers, settings.Version))

			id := "legacy-queued-ideation"
			input := map[string]string{"project_id": f.project.ID, "day": "2026-11-24"}
			op := model.Operation{Kind: "ideation", Subject: f.project.ID, Owner: tc.originalOwner, Stage: "queued", Input: input}
			requireWorkflowSuccess(t, engine.Store.Put(ctx, "operations", id, op, 0))
			op.Owner = "prior-generation-owner"
			requireWorkflowSuccess(t, engine.Store.Put(ctx, "operations", id, op, 1))
			engine.dispatchQueuedIdeation(ctx)
			engine.background.Wait()
			saved, _ := inspectOnboardingIdeation(t, engine, id)
			if tc.wantAutomatic {
				if saved.Stage != "queued" || !saved.RegistrationIdeation() || !strings.Contains(saved.Error, "on_registration") || f.fixture.generations != 0 {
					t.Fatalf("previously requeued registration started or lost its trigger: %+v; generations=%d", saved, f.fixture.generations)
				}
			} else if saved.Stage != "complete" || saved.RegistrationIdeation() || f.fixture.generations != 1 {
				t.Fatalf("manual operation was mistaken for automatic registration: %+v; generations=%d", saved, f.fixture.generations)
			}
		})
	}
}
