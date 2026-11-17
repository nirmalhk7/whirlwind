package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"

	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/verification"
)

// Raw analyzer failures must remain failures in the journal while unchanged,
// complete findings can qualify against the approved base at the review boundary.
func TestParityQltyBaselineAdvisoryReachesReviewerWithoutRewritingRawResults(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := context.Background()
	project, err := e.Project(ctx, idea.ProjectID)
	requireWorkflowSuccess(t, err)
	tool := filepath.Join(t.TempDir(), "qlty")
	const sarif = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"fixture-quality"}},"invocations":[{"executionSuccessful":true}],"results":[{"ruleId":"existing","level":"warning","message":{"text":"Existing finding"},"partialFingerprints":{"primaryLocationLineHash":"stable"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"file.txt"},"region":{"startLine":1}}}]}]}]}`
	requireWorkflowSuccess(t, os.WriteFile(tool, []byte("#!/bin/sh\nprintf '%s\\n' '"+sarif+"'\nexit 1\n"), 0700))
	project.Policy.Checks = append(project.Policy.Checks, model.Check{Name: "quality", Command: []string{tool, "check", "--sarif"}, TimeoutSeconds: 5, ReportFormat: "sarif"})
	if project.Policy.Sandbox == nil {
		project.Policy.Sandbox = &model.SandboxPolicy{}
	}
	project.Policy.Sandbox.ReadRoots = append(project.Policy.Sandbox.ReadRoots, tool)
	qualityRoot := t.TempDir()
	requireWorkflowSuccess(t, os.MkdirAll(filepath.Join(qualityRoot, "config", ".qlty"), 0700))
	requireWorkflowSuccess(t, os.WriteFile(filepath.Join(qualityRoot, "config", ".qlty", "qlty.toml"), []byte("config_version = \"0\"\n"), 0600))
	project.Policy.Quality = &model.QualityPolicy{Root: qualityRoot, ConfigurationFiles: []string{".qlty/qlty.toml"}}
	data, err := yaml.Marshal(project.Policy)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, os.WriteFile(filepath.Join(project.Path, ".whirlwind.yaml"), data, 0600))
	submissionGit(t, project.Path, "add", ".whirlwind.yaml")
	submissionGit(t, project.Path, "commit", "-m", "approve quality command")
	project.Policy, project.PolicyHash, err = policy.Load(project.Path)
	requireWorkflowSuccess(t, err)
	row, _ := e.Store.Get(ctx, "projects", project.ID)
	requireWorkflowSuccess(t, e.Store.Put(ctx, "projects", project.ID, project, row.Version))
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.intakeApprovedIdeas(ctx)
	requireWorkflowSuccess(t, e.Step(ctx, model.PlanID("idea:"+idea.ID)))
	plan, version := localStoredTask(t, e, model.PlanID("idea:"+idea.ID))
	requireWorkflowSuccess(t, e.ReviewLocalPlan(ctx, plan.ID, version, "approve", plan.PlanDocument.Hash, ""))
	plan, _ = localStoredTask(t, e, plan.ID)
	installNativeParityVerifier(t)
	plan.Worktree = project.Path
	plan.Base = submissionGit(t, project.Path, "rev-parse", "HEAD")
	requireWorkflowSuccess(t, os.WriteFile(filepath.Join(project.Path, "result.txt"), []byte("result\n"), 0600))
	submissionGit(t, project.Path, "add", "result.txt")
	submissionGit(t, project.Path, "commit", "-m", "new saved code")
	head := submissionGit(t, project.Path, "rev-parse", "HEAD")
	if err := e.verify(ctx, project, &plan); err != nil {
		t.Fatalf("unchanged baseline findings blocked verification: %v", err)
	}
	record, err := e.Store.Get(ctx, "verification", plan.VerificationID)
	requireWorkflowSuccess(t, err)
	var saved struct {
		Raw    verification.JobReport `json:"runner_report"`
		Report verification.Report    `json:"report"`
	}
	requireWorkflowSuccess(t, json.Unmarshal(record.Data, &saved))
	if saved.Raw.Passed || saved.Raw.Results[1].Status != "fail" || !saved.Report.Passed {
		t.Fatalf("raw/qualified outcomes lost: %+v", saved)
	}
	step := taskStep{Engine: e, ctx: ctx, task: plan, project: project}
	if err := step.validateReviewRunnerReport(head); err != nil {
		t.Fatalf("accepted baseline cannot reach Reviewer: %v", err)
	}
	provenance := model.Provenance{Head: head}
	proof := runtimeProofInput{task: plan, provenance: provenance, check: project.Policy.Checks[1], result: saved.Report.Results[1]}
	if err := e.validateStandaloneCheckProof(ctx, proof); err != nil {
		t.Fatalf("qualified proof rejected: %v", err)
	}
	proof.result.Output = strings.ReplaceAll(proof.result.Output, "Existing finding", "Forged accepted finding")
	if err := e.validateStandaloneCheckProof(ctx, proof); err == nil {
		t.Fatal("advisory marker hid altered scanner evidence")
	}
	// A cached acceptance boolean cannot substitute for the approved baseline.
	rows, err := e.Store.List(ctx, "quality_baselines")
	if err != nil || len(rows) != 1 {
		t.Fatalf("baseline missing: %v %v", rows, err)
	}
	if _, err := e.Store.DB.ExecContext(ctx, "DELETE FROM resources WHERE kind='quality_baselines'"); err != nil {
		t.Fatal(err)
	}
	if err := step.validateReviewRunnerReport(head); err == nil {
		t.Fatal("Reviewer trusted acceptance without retained baseline")
	}
}
