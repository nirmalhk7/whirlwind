package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"

	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/verification"
)

func optionalCheckFixture(t *testing.T, optionalExit string, additional ...model.Check) (*Engine, model.Project, model.ExecutionPlan, string) {
	t.Helper()
	e, idea := approvedIdeaPlannerFixture(t)
	installNativeParityVerifier(t)
	ctx := context.Background()
	optional := false
	checks := append([]model.Check{{Name: "advisory", Command: []string{"sh", "-c", "exit " + optionalExit}, TimeoutSeconds: 5, Required: &optional}}, additional...)
	project := configureQualifiedFixtureChecks(t, e, idea.ProjectID, checks)
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.intakeApprovedIdeas(ctx)
	id := model.PlanID("idea:" + idea.ID)
	if err := e.Step(ctx, id); err != nil {
		t.Fatal(err)
	}
	plan, version := localStoredTask(t, e, id)
	if err := e.ReviewLocalPlan(ctx, plan.ID, version, "approve", plan.PlanDocument.Hash, ""); err != nil {
		t.Fatal(err)
	}
	plan, _ = localStoredTask(t, e, id)
	plan.Worktree = project.Path
	plan.Base = submissionGit(t, project.Path, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(project.Path, "result.txt"), []byte("result\n"), 0600); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, project.Path, "add", "result.txt")
	submissionGit(t, project.Path, "commit", "-m", "implement approved result")
	return e, project, plan, submissionGit(t, project.Path, "rev-parse", "HEAD")
}

func configureFixtureChecks(t *testing.T, e *Engine, projectID string, checks []model.Check) model.Project {
	t.Helper()
	ctx := context.Background()
	project, err := e.Project(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Policy.Checks = append(project.Policy.Checks, checks...)
	optionalFixtureQuality(t, &project, checks)
	data, err := yaml.Marshal(project.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project.Path, ".whirlwind.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, project.Path, "add", ".whirlwind.yaml")
	submissionGit(t, project.Path, "commit", "-m", "approve verification commands")
	project.Policy, project.PolicyHash, err = policy.Load(project.Path)
	if err != nil {
		t.Fatal(err)
	}
	row, err := e.Store.Get(ctx, "projects", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "projects", project.ID, project, row.Version); err != nil {
		t.Fatal(err)
	}
	return project
}

func configureQualifiedFixtureChecks(t *testing.T, e *Engine, projectID string, checks []model.Check) model.Project {
	t.Helper()
	project := configureFixtureChecks(t, e, projectID, checks)
	for _, check := range checks {
		if !isQltyCommand(check.Command) {
			continue
		}
		project.QualityIdentity = qualifyNativeFixtureScanner(t, project, check)
		row, err := e.Store.Get(t.Context(), "projects", projectID)
		requireWorkflowSuccess(t, err)
		requireWorkflowSuccess(t, e.Store.Put(t.Context(), "projects", projectID, project, row.Version))
		break
	}
	return project
}

func optionalFixtureQuality(t *testing.T, project *model.Project, checks []model.Check) {
	t.Helper()
	qualityChecks := []model.Check{}
	for _, check := range checks {
		if isQltyCommand(check.Command) {
			qualityChecks = append(qualityChecks, check)
		}
	}
	if len(qualityChecks) == 0 {
		return
	}
	if project.Policy.Sandbox == nil {
		project.Policy.Sandbox = &model.SandboxPolicy{}
	}
	for _, check := range qualityChecks {
		if filepath.IsAbs(check.Command[0]) {
			project.Policy.Sandbox.ReadRoots = append(project.Policy.Sandbox.ReadRoots, check.Command[0])
		}
	}
	root := t.TempDir()
	t.Cleanup(func() {
		if err := filepath.Walk(root, restoreAgenticCachePathPermissions); err != nil {
			t.Errorf("restore permissions for disposable scanner cache: %v", err)
		}
	})
	if err := os.MkdirAll(filepath.Join(root, "config", ".qlty"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", ".qlty", "qlty.toml"), []byte("config_version = \"0\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	project.Policy.Quality = &model.QualityPolicy{Root: root, ConfigurationFiles: []string{".qlty/qlty.toml"}}
}

func TestParityOptionalCommandFailureReachesReviewWithRawEvidence(t *testing.T) {
	e, project, plan, head := optionalCheckFixture(t, "1")
	ctx := context.Background()
	if err := e.verify(ctx, project, &plan); err != nil {
		t.Fatalf("optional command blocked required verification: %v", err)
	}
	row, err := e.Store.Get(ctx, "verification", plan.VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Raw    verification.JobReport `json:"runner_report"`
		Report verification.Report    `json:"report"`
	}
	if err := json.Unmarshal(row.Data, &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Raw.Passed || !saved.Report.Passed || len(saved.Raw.Results) != 2 || saved.Raw.Results[1].Passed || saved.Raw.Results[1].Status != "fail" || saved.Report.Results[1].Passed {
		t.Fatalf("optional failure was lost or falsely relabeled: %+v", saved)
	}
	step := taskStep{Engine: e, ctx: ctx, task: plan, project: project}
	if err := step.validateReviewRunnerReport(head); err != nil {
		t.Fatalf("review rejected complete optional evidence: %v", err)
	}
	passed, err := e.approvedChecksPassed(ctx, plan, model.Provenance{Head: head}, project.Policy.Checks, saved.Report)
	if err != nil || !passed {
		t.Fatalf("acceptance rejected optional failure: passed=%v err=%v", passed, err)
	}
	missing := saved.Report
	missing.Results = append([]verification.Result{}, saved.Report.Results[:1]...)
	if passed, err := e.approvedChecksPassed(ctx, plan, model.Provenance{Head: head}, project.Policy.Checks, missing); err == nil || passed {
		t.Fatal("optional designation allowed missing execution evidence")
	}
}

func TestParityOptionalFailureAndExistingQualityDebtReachReview(t *testing.T) {
	for _, exits := range []struct{ quality, optional string }{{"0", "1"}, {"1", "1"}, {"1", "7"}} {
		t.Run("quality_exit_"+exits.quality+"_optional_exit_"+exits.optional, func(t *testing.T) { assertOptionalQualityReview(t, exits.quality, exits.optional) })
	}
}

func assertOptionalQualityReview(t *testing.T, exit, optionalExit string) {
	t.Helper()
	const findings = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty","version":"fixture","semanticVersion":"1.0.0"}},"invocations":[{"executionSuccessful":true}],"results":[{"ruleId":"existing","level":"warning","message":{"text":"Existing quality finding"},"partialFingerprints":{"primaryLocationLineHash":"same"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"file.txt"},"region":{"startLine":1}}}]}]}]}`
	tool := writeQltyFixture(t, findings, exit, "")
	e, project, plan, head := optionalCheckFixture(t, optionalExit, model.Check{Name: "quality", Command: []string{tool, "check", "--sarif"}, TimeoutSeconds: 5, ReportFormat: "sarif"})
	ctx := context.Background()
	if err := e.verify(ctx, project, &plan); err != nil {
		t.Fatalf("optional failure with unchanged quality debt blocked verification: %v", err)
	}
	step := taskStep{Engine: e, ctx: ctx, task: plan, project: project}
	if err := step.validateReviewRunnerReport(head); err != nil {
		t.Fatalf("review lost frozen optional command policy: %v", err)
	}
	row, err := e.Store.Get(ctx, "verification", plan.VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Report verification.Report `json:"report"`
	}
	if err := json.Unmarshal(row.Data, &saved); err != nil {
		t.Fatal(err)
	}
	passed, err := e.approvedChecksPassed(ctx, plan, model.Provenance{Head: head}, project.Policy.Checks, saved.Report)
	if err != nil || !passed {
		t.Fatalf("acceptance lost qualified baseline evidence: passed=%v err=%v", passed, err)
	}
}
