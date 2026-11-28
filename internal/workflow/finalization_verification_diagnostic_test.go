package workflow

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func TestFinalizationVerificationKeepsRecordedPrerequisiteFailure(t *testing.T) {
	e, plan, project, record := acceptedDeliveryFixture(t)
	ctx := t.Context()
	row, err := e.Store.Get(ctx, "verification", plan.VerificationID)
	requireWorkflowSuccess(t, err)
	failed, err := store.Decode[map[string]any](row)
	requireWorkflowSuccess(t, err)
	failed["stage"], failed["error"] = "failed", "qualified scanner runtime was not recorded; rerun project setup"
	requireWorkflowSuccess(t, e.Store.Put(ctx, "verification", row.ID, failed, row.Version))

	_, _, accepted, err := e.LoadAcceptedReviewReport(ctx, plan.ID)
	if accepted || err == nil || !strings.Contains(err.Error(), "qualified scanner runtime was not recorded") {
		t.Fatalf("acceptance lost its precise verification prerequisite: accepted=%v err=%v", accepted, err)
	}
	step := taskStep{Engine: e, ctx: ctx, task: plan, project: project, record: record}
	if _, err := step.submitting(); err == nil || !strings.Contains(err.Error(), "qualified scanner runtime was not recorded") || step.task.Delivery != nil {
		t.Fatalf("Finalizer did not refuse the failed verification: delivery=%+v error=%v", step.task.Delivery, err)
	}
	refs := submissionGit(t, project.Path, "for-each-ref", "--format=%(refname)", "refs/heads/codex/delivery")
	if refs != "" {
		t.Fatalf("invalid evidence created a delivery ref: %s", refs)
	}
}

func TestFinalizerRejectsFailedMandatoryStaticCheck(t *testing.T) {
	e, plan, project, record := acceptedDeliveryFixture(t)
	ctx := t.Context()
	_, _, accepted, err := e.LoadAcceptedReviewReport(ctx, plan.ID)
	requireWorkflowSuccess(t, err)
	if !accepted {
		t.Fatal("the green fixture must be eligible for finalization")
	}
	row, err := e.Store.Get(ctx, "verification", plan.VerificationID)
	requireWorkflowSuccess(t, err)
	evidence, err := store.Decode[map[string]any](row)
	requireWorkflowSuccess(t, err)
	var saved struct {
		Report verification.Report `json:"report"`
	}
	requireWorkflowSuccess(t, json.Unmarshal(row.Data, &saved))
	report := saved.Report
	static := &report.Results[qltyResultIndex(t, report)]
	static.Passed, static.ExitCode = false, intPointer(1)
	report.Passed = false
	evidence["report"] = report
	requireWorkflowSuccess(t, e.Store.Put(ctx, "verification", row.ID, evidence, row.Version))

	step := taskStep{Engine: e, ctx: ctx, task: plan, project: project, record: record}
	if _, err := step.submitting(); err == nil || step.task.Delivery != nil {
		t.Fatalf("Finalizer accepted failed %s evidence: delivery=%+v error=%v", static.Name, step.task.Delivery, err)
	}
	if refs := submissionGit(t, project.Path, "for-each-ref", "--format=%(refname)", "refs/heads/codex/delivery"); refs != "" {
		t.Fatalf("failed static check created a delivery ref: %s", refs)
	}
	t.Logf("Finalizer refused %s exit=%d, verification=%s, candidate=%s", static.Name, *static.ExitCode, row.ID, plan.ReviewedCommit)
}

func TestFinalizationVerificationNamesMissingConfiguredRunner(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "missing-verifier")
	t.Setenv("WHIRLWIND_VERIFY_BIN", binary)
	if _, err := localVerifierBinary(); err == nil || !strings.Contains(err.Error(), binary) {
		t.Fatalf("missing configured verifier did not identify the prerequisite: %v", err)
	}
}

func TestFinalizationVerificationPersistsBaselineSetupFailure(t *testing.T) {
	e, plan := approvedCodingPairFixture(t, model.Check{Name: "baseline-check", Command: []string{"/usr/bin/true"}, TimeoutSeconds: 5})
	ctx := t.Context()
	project, err := e.Project(ctx, plan.ProjectID)
	requireWorkflowSuccess(t, err)
	plan.Worktree = project.Path
	plan.Base = submissionGit(t, project.Path, gitRevParseCommand, "HEAD")
	requireWorkflowSuccess(t, os.WriteFile(filepath.Join(project.Path, "result.txt"), []byte("candidate fixture\n"), 0600))
	submissionGit(t, project.Path, "add", "result.txt")
	submissionGit(t, project.Path, "commit", "-m", "verified candidate fixture")
	binary := filepath.Join(t.TempDir(), "missing-verifier")
	t.Setenv("WHIRLWIND_VERIFY_BIN", binary)
	err = e.verify(ctx, project, &plan)
	if !errors.Is(err, verification.ErrJobSetup) {
		t.Fatalf("verify did not preserve setup failure class: %v", err)
	}
	row, err := e.Store.Get(ctx, "verification", plan.VerificationID)
	requireWorkflowSuccess(t, err)
	failed, err := store.Decode[map[string]any](row)
	requireWorkflowSuccess(t, err)
	reason, _ := failed["error"].(string)
	if failed["stage"] != "failed" || !strings.Contains(reason, binary) {
		t.Fatalf("candidate verification was not finalized with the exact blocker: stage=%v error=%v", failed["stage"], failed["error"])
	}
}

func TestFinalizationVerificationRejectsStaleOrMissingRecordsSpecifically(t *testing.T) {
	e, plan, project, _ := acceptedDeliveryFixture(t)
	ctx := t.Context()
	row, err := e.Store.Get(ctx, "verification", plan.VerificationID)
	requireWorkflowSuccess(t, err)
	stale, err := store.Decode[map[string]any](row)
	requireWorkflowSuccess(t, err)
	stale["stage"], stale["error"] = "failed", "from a different candidate"
	provenance := stale["provenance"].(map[string]any)
	provenance["head"] = "different-head"
	requireWorkflowSuccess(t, e.Store.Put(ctx, "verification", row.ID, stale, row.Version))
	_, _, err = e.loadVerificationEvidence(ctx, plan, project)
	if err == nil || !strings.Contains(err.Error(), "does not match the reviewed candidate") || strings.Contains(err.Error(), "from a different candidate") {
		t.Fatalf("stale evidence was presented as the current candidate's blocker: %v", err)
	}

	plan.VerificationID = "missing-verification"
	_, _, err = e.loadVerificationEvidence(ctx, plan, project)
	if !errors.Is(err, store.ErrNotFound) || !strings.Contains(err.Error(), plan.VerificationID) {
		t.Fatalf("missing verification record had no actionable identity: %v", err)
	}
}

func TestFinalizationVerificationQualificationIsRequiredWithoutRunningAHostCommand(t *testing.T) {
	// The original scoped-finalizer fixture uses this scanner stub. It
	// supplies frozen config, but has not registered a qualified tool runtime.
	// Inspect the exact fixture approval/manifest definition on any host.
	scanner := cleanQltyFixtureCheck(t)
	e, plan := approvedCodingPairFixture(t, scanner)
	project, err := e.Project(t.Context(), plan.ProjectID)
	requireWorkflowSuccess(t, err)
	quality, err := approvedQualitySnapshot(plan)
	requireWorkflowSuccess(t, err)
	if quality == nil || quality.Identity != nil {
		t.Fatal("fixture no longer represents approved config without qualified tools")
	}
	head := submissionGit(t, project.Path, gitRevParseCommand, "HEAD")
	tree := submissionGit(t, project.Path, gitRevParseCommand, "HEAD^{tree}")
	const jobID = "finalization-fixture"
	manifest := verification.JobManifest{Version: 1, JobID: jobID, PlanID: plan.ID, PlanVersion: plan.PlanVersion,
		PlanHash: plan.PlanDocument.ApprovedHash, ConfigurationHash: verificationConfigurationHash(plan), CandidateCommit: head, BaseCommit: head, CandidateTree: tree,
		Root: filepath.Join(quality.Root, "jobs", jobID), Repository: project.Path, Launcher: "/fixture/launcher", Quality: quality,
		Commands: []verification.JobCommand{{Name: scanner.Name, Command: scanner.Command, Mode: scanner.Mode, TimeoutSeconds: scanner.TimeoutSeconds, ReportFormat: scanner.ReportFormat}}}
	err = (verification.JobReport{}).Validate(manifest)
	if err == nil || !strings.Contains(err.Error(), "verification with Qlty requires an approved runtime identity") {
		t.Fatalf("could not isolate the missing native fixture prerequisite: %v", err)
	}
}
