package store

import (
	"context"
	"testing"
	"whirlwind/internal/model"
)

const (
	planFixtureDocumentBytes = "document-bytes"
)

func TestParityPhysicalPlanTablesSeparateAuthoringApprovalAndExecution(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	plan := model.ExecutionPlan{ID: "plan", ProjectID: "project", PlanVersion: 1, Stage: "plan_approval", PlanDocument: &model.PlanDocument{Path: "/plans/project.md", Hash: planFixtureDocumentBytes}}
	if err := s.Put(ctx, "execution_plans", plan.ID, plan, 0); err != nil {
		t.Fatal(err)
	}
	var authoring, approval, stage string
	if err := s.DB.QueryRow("SELECT CASE WHEN json_extract(data,'$.plan_document.hash') IS NOT NULL THEN 'completed' ELSE 'pending' END,CASE WHEN json_extract(data,'$.plan_document.approved_hash')=json_extract(data,'$.plan_document.hash') THEN 'approved' ELSE 'unapproved' END,json_extract(data,'$.stage') FROM resources WHERE kind='execution_plans' AND id='plan'").Scan(&authoring, &approval, &stage); err != nil || authoring != "completed" || approval != "unapproved" || stage != "plan_approval" {
		t.Fatalf("authoring completion falsely approved or missing: %s %s %s %v", authoring, approval, stage, err)
	}
	frozen := model.PlanSpecification{Specification: "# Exact approved bytes\n", Requirements: []model.Requirement{{ID: "REQ-1", Outcome: "Return result", EvidenceMethod: "Run result test"}}, Approval: &model.PlanApproval{DocumentHash: planFixtureDocumentBytes, PlanVersion: 1, ConfigurationHash: "human-config"}}
	plan.Plan = &frozen
	plan.PlanDocument.ApprovedHash = planFixtureDocumentBytes
	plan.Stage = "coding"
	if err := s.Write(ctx, []Change{{Kind: "plan_versions", ID: "plan:1", Value: frozen}, {Kind: "execution_plans", ID: plan.ID, Value: plan, Expected: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	var specification, documentHash, configurationHash, requirements string
	if err := s.DB.QueryRow("SELECT specification,document_hash,configuration_hash,requirements FROM plan_versions WHERE plan_id='plan' AND plan_version=1").Scan(&specification, &documentHash, &configurationHash, &requirements); err != nil || specification != "# Exact approved bytes\n" || documentHash != planFixtureDocumentBytes || configurationHash != "human-config" {
		t.Fatalf("approved bytes/configuration not persisted in SQL: %q %q %q %s %v", specification, documentHash, configurationHash, requirements, err)
	}
	if err := s.DB.QueryRow("SELECT CASE WHEN json_extract(data,'$.plan_document.hash') IS NOT NULL THEN 'completed' ELSE 'pending' END,CASE WHEN json_extract(data,'$.plan_document.approved_hash')=json_extract(data,'$.plan_document.hash') THEN 'approved' ELSE 'unapproved' END,json_extract(data,'$.stage') FROM resources WHERE kind='execution_plans' AND id='plan'").Scan(&authoring, &approval, &stage); err != nil || authoring != "completed" || approval != "approved" || stage != "coding" {
		t.Fatalf("separate approval/execution missing: %s %s %s %v", authoring, approval, stage, err)
	}
	for _, sql := range []string{"UPDATE plan_versions SET specification='changed' WHERE plan_id='plan'", "DELETE FROM plan_versions WHERE plan_id='plan'"} {
		if _, err := s.DB.Exec(sql); err == nil {
			t.Fatalf("SQL permitted changing immutable approved specification: %s", sql)
		}
	}
}

func TestParityPhysicalPlanWritesRollbackWithStaleVersion(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	plan := model.ExecutionPlan{ID: "plan", ProjectID: "project", PlanVersion: 1, Stage: "coding"}
	if err := s.Put(ctx, "execution_plans", plan.ID, plan, 0); err != nil {
		t.Fatal(err)
	}
	plan.Stage = "complete"
	if err := s.Write(ctx, []Change{{Kind: "plan_versions", ID: "plan:1", Value: model.PlanSpecification{Specification: "Must roll back"}}, {Kind: "execution_plans", ID: "plan", Value: plan, Expected: 9}}, nil); err == nil {
		t.Fatal("stale update committed")
	}
	var stage string
	var versions int
	if err := s.DB.QueryRow("SELECT json_extract(data,'$.stage') FROM resources WHERE kind='execution_plans' AND id='plan'").Scan(&stage); err != nil || stage != "coding" {
		t.Fatalf("stale write changed physical state: %s %v", stage, err)
	}
	if err := s.DB.QueryRow("SELECT count(*) FROM plan_versions WHERE plan_id='plan'").Scan(&versions); err != nil || versions != 0 {
		t.Fatalf("uncommitted approval leaked into physical table: %d %v", versions, err)
	}
}

func TestParitySQLIdeaLinkProtectsItsPhysicalExecutionPlan(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO plans(plan_id,idea_id,plan_path,status,timestamp) VALUES('plan','i','/plans/plan.md','draft','now')"); err != nil {
		t.Fatal(err)
	}
	idea["plan_id"] = "plan"
	if err := s.Put(ctx, "ideas", "i", idea, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("DELETE FROM plans WHERE plan_id='plan'"); err == nil {
		t.Fatal("linked physical execution plan could be deleted")
	}

}

func TestParitySQLPlannerRejectsWrongSourceRunAndIncompleteCompletion(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO ideation_runs(run_id,project_id,status,created_at) VALUES('other','other-project','completed','now')"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "sessions", "attempt", model.Attempt{AttemptID: "attempt", Role: "plan"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO planner_runs(planner_run_id,idea_id,idea_run_id,timestamp,status) VALUES('attempt','i','other','now','in_process')"); err == nil {
		t.Error("Planner SQL row accepted another idea's source run")
		if _, err := s.DB.Exec("DELETE FROM planner_runs WHERE planner_run_id='attempt'"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec("INSERT INTO planner_runs(planner_run_id,idea_id,idea_run_id,timestamp,status) VALUES('attempt','i','source','now','in_process')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE planner_runs SET status='completed' WHERE planner_run_id='attempt'"); err == nil {
		t.Fatal("Planner SQL row completed without Markdown, linked plan or completion time")
	}
}

func TestParitySQLExecutionPlanRejectsForeignIdeaSource(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "execution_plans", "plan", model.ExecutionPlan{ID: "plan", ProjectID: "p", IdeaID: "i", IdeationRunID: "source", Stage: "planning", PlanVersion: 1}, 0); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct{ project, run, idea string }{
		{"other", "source", "i"}, {"p", "other", "i"}, {"p", "source", "missing"}, {"p", "", "i"},
	} {
		plan := model.ExecutionPlan{ID: "plan", ProjectID: mutation.project, IdeationRunID: mutation.run, IdeaID: mutation.idea, Stage: "planning", PlanVersion: 1}
		if err := s.Put(ctx, "execution_plans", "plan", plan, 1); err == nil {
			t.Fatalf("plan accepted inconsistent source: %+v", mutation)
		}
	}
}
