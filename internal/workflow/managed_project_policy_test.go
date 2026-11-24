package workflow

import (
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
)

func TestManagedPolicyProtectsPlanApprovalAndExecution(t *testing.T) {
	ctx := t.Context()
	engine, task := localPlanFixture(t)
	record, err := engine.Store.Get(ctx, "projects", task.ProjectID)
	requireWorkflowSuccess(t, err)
	project, err := store.Decode[model.Project](record)
	requireWorkflowSuccess(t, err)
	const text = "allowed_paths: [result.txt]\nrisk_rules: [{level: low, paths: [result.txt]}]\nchecks: [{name: result, command: [test, -f, result.txt]}]\n"
	rules, hash, err := policy.ParseManagedPolicy(project.ID, text)
	requireWorkflowSuccess(t, err)
	project.Policy, project.PolicyHash, project.ManagedPolicy, project.Status = rules, hash, true, "draft"
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "projects", project.ID, project, record.Version))
	view, err := engine.ReadLocalPlan(ctx, task.ID)
	requireWorkflowSuccess(t, err)
	_, taskVersion := localStoredTask(t, engine, task.ID)
	if err := engine.ReviewLocalPlan(ctx, task.ID, taskVersion, "approve", view.Hash, ""); err == nil {
		t.Fatal("plan approved without the approved policy bytes")
	}
	document := model.ProjectPolicyDocument{ProjectID: project.ID, Content: text}
	requireWorkflowSuccess(t, engine.Store.Put(ctx, policy.ManagedPolicyResource, project.ID, document, 0))
	if err := engine.ReviewLocalPlan(ctx, task.ID, taskVersion, "approve", view.Hash, ""); err == nil || !strings.Contains(err.Error(), "finish project setup") {
		t.Fatalf("plan approval did not wait for setup: %v", err)
	}
	project.Status = "approved"
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "projects", project.ID, project, record.Version+1))
	requireWorkflowSuccess(t, engine.ReviewLocalPlan(ctx, task.ID, taskVersion, "approve", view.Hash, ""))
	approved, _ := localStoredTask(t, engine, task.ID)
	if approved.PlanDocument.PolicyHash != hash {
		t.Fatal("plan approval did not freeze the managed policy fingerprint")
	}
	requireWorkflowSuccess(t, engine.ValidateLocalPlan(ctx, approved))
	document.Content += "# Unapproved edit\n"
	requireWorkflowSuccess(t, engine.Store.Put(ctx, policy.ManagedPolicyResource, project.ID, document, 1))
	if err := engine.ValidateLocalPlan(ctx, approved); err == nil || !strings.Contains(err.Error(), "policy approval is stale") {
		t.Fatalf("plan accepted unapproved policy content: %v", err)
	}
}
