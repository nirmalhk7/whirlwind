package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestParityApprovalFreezesConfigurationReferences(t *testing.T) {
	e, plan := localPlanFixture(t)
	ctx := context.Background()
	view, err := e.ReadLocalPlan(ctx, plan.ID)
	requireWorkflowSuccess(t, err)
	_, version := localStoredTask(t, e, plan.ID)
	requireWorkflowSuccess(t, e.ReviewLocalPlan(ctx, plan.ID, version, "approve", view.Hash, ""))
	record, err := e.Store.Get(ctx, "plan_versions", plan.ID+":1")
	requireWorkflowSuccess(t, err)
	var frozen struct {
		Approval struct {
			DocumentHash      string `json:"document_hash"`
			PlanVersion       int    `json:"plan_version"`
			ConfigurationHash string `json:"configuration_hash"`
			References        []struct {
				Kind, ID string
				Version  int
				Hash     string
				Data     json.RawMessage
			} `json:"references"`
		} `json:"approval"`
	}
	requireWorkflowSuccess(t, json.Unmarshal(record.Data, &frozen))
	if frozen.Approval.DocumentHash != view.Hash || frozen.Approval.PlanVersion != 1 || len(frozen.Approval.ConfigurationHash) != 64 {
		t.Fatalf("approval lacks immutable document/configuration identity: %+v", frozen.Approval)
	}
	roles := map[string]bool{}
	for _, ref := range frozen.Approval.References {
		if ref.Version < 1 || len(ref.Hash) != 64 || !json.Valid(ref.Data) {
			t.Fatalf("incomplete configuration reference: %+v", ref)
		}
		if ref.Kind == "agents" {
			roles[ref.ID] = true
		}
	}
	for _, role := range model.AgentRoles() {
		if !roles[role] {
			t.Fatalf("role %s not frozen", role)
		}
	}
	// Changing a role queue cannot silently authorize a different model.
	agentRecord, err := e.Store.Get(ctx, "agents", "code")
	requireWorkflowSuccess(t, err)
	agent, _ := store.Decode[model.AgentConfig](agentRecord)
	agent.Chain[0].Thinking = &model.ThinkingChoice{ConfigID: "effort", Value: "changed-after-approval"}
	requireWorkflowSuccess(t, e.Store.Put(ctx, "agents", "code", agent, agentRecord.Version))
	current, _ := localStoredTask(t, e, plan.ID)
	if err := e.ValidateLocalPlan(ctx, current); err == nil {
		t.Fatal("changed execution configuration retained approval")
	}
	invalidated, _ := localStoredTask(t, e, plan.ID)
	if invalidated.Stage != "plan_approval" || invalidated.PlanDocument.ApprovedHash != "" {
		t.Fatalf("changed configuration has no human reapproval path: stage=%s hash=%s", invalidated.Stage, invalidated.PlanDocument.ApprovedHash)
	}
	assertFrozenApprovalUnchanged(t, ctx, e.Store, plan.ID, record.Data)
}

func TestParityApprovalConfigurationRaceRollsBack(t *testing.T) {
	e, plan := localPlanFixture(t)
	ctx := context.Background()
	view, err := e.ReadLocalPlan(ctx, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, version := localStoredTask(t, e, plan.ID)
	staged, batch := store.Stage(ctx)
	if err := e.ReviewLocalPlan(staged, plan.ID, version, "approve", view.Hash, ""); err != nil {
		t.Fatal(err)
	}
	r, err := e.Store.Get(ctx, "agents", "code")
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := store.Decode[model.AgentConfig](r)
	agent.Chain[0].Model = "new-model-after-inspection"
	if err := e.Store.Put(ctx, "agents", "code", agent, r.Version); err != nil {
		t.Fatal(err)
	}
	if err := batch.Commit(staged, e.Store, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("configuration race approved: %v", err)
	}
	current, currentVersion := localStoredTask(t, e, plan.ID)
	if currentVersion != version || current.Stage != "plan_approval" || current.PlanDocument.ApprovedHash != "" {
		t.Fatal("stale approval escaped transaction")
	}
	if _, err := e.Store.Get(ctx, "plan_versions", plan.ID+":1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("stale approval published immutable record", err)
	}
}

func assertFrozenApprovalUnchanged(t *testing.T, ctx context.Context, database *store.Store, id string, data []byte) {
	t.Helper()
	after, err := database.Get(ctx, "plan_versions", id+":1")
	if err != nil || string(after.Data) != string(data) {
		t.Fatal("configuration change rewrote immutable approval", err)
	}
}
