package workflow

import (
	"context"
	"fmt"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

const acceptedCandidateRevision = "candidate-sha"

type acceptedReviewFixture struct {
	evidence map[string]any
	ctx      context.Context
	engine   *Engine
	task     model.Task
	project  model.Project
	report   verification.Report
	version  int
}

func newAcceptedReviewFixture(t *testing.T) *acceptedReviewFixture {
	t.Helper()
	ctx := context.Background()
	e, task := localPlanFixture(t)
	view, err := e.ReadLocalPlan(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, version := localStoredTask(t, e, task.ID)
	if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""); err != nil {
		t.Fatal(err)
	}
	task, version = localStoredTask(t, e, task.ID)
	checkpoint := task.Plan.Units[0]
	checkpoint.Stage, checkpoint.AttemptID = "integrated", "attempt-1"
	checkpoint.Base, checkpoint.Worktree = "base-sha", "/tmp/coded-worktree"
	checkpoint.Branch, checkpoint.Commit = "feature/example", acceptedCandidateRevision
	if err := e.Store.Put(ctx, "units", unitKey(task.ID, task.PlanVersion, checkpoint.ID), checkpoint, 0); err != nil {
		t.Fatal(err)
	}
	if completed := e.restoreUnits(ctx, &task); !completed[checkpoint.ID] || task.Plan.Units[0].Commit != acceptedCandidateRevision {
		t.Fatalf("integrated coder checkpoint was not restored: completed=%v unit=%+v", completed, task.Plan.Units[0])
	}
	task.ChecksPassed = true
	task.Base, task.ReviewedCommit, task.VerificationID = "base-sha", acceptedCandidateRevision, "verification-1"
	task.ReviewID = task.ID + ":1:1"
	if err := e.Store.Put(ctx, "tasks", task.ID, task, version); err != nil {
		t.Fatal(err)
	}
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	provenance := acceptedReviewProvenance(task, project.PolicyHash)
	evidence := map[string]any{"stage": "complete", "risk": task.Risk, "report": verification.Report{Passed: true}, "provenance": provenance}
	if err := e.Store.Put(ctx, "verification", task.VerificationID, evidence, 0); err != nil {
		t.Fatal(err)
	}
	review := model.Review{Requirements: []model.RequirementResult{{ID: "REQ-RESULT", Status: "pass", Code: []string{"result.go:12"}, Evidence: []string{"TestResultContainsExpectedValue passed"}, Explanation: "The required result is returned."}}, Provenance: &provenance}
	if err := e.Store.Put(ctx, "reviews", task.ReviewID, review, 0); err != nil {
		t.Fatal(err)
	}
	f := &acceptedReviewFixture{ctx: ctx, engine: e, task: task, project: project, evidence: evidence, version: 1}
	f.report = f.completeReport(t)
	return f
}

func acceptedReviewProvenance(task model.Task, policyHash string) model.Provenance {
	return model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Head: task.ReviewedCommit, Base: task.Base,
		PolicyHash: policyHash, VerificationID: task.VerificationID, ReviewID: task.ReviewID}
}

func (f *acceptedReviewFixture) completeReport(t *testing.T) verification.Report {
	t.Helper()
	checks, err := planVerificationChecks(f.project.Policy, *f.task.Plan)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	report := verification.Report{Passed: true, Results: make([]verification.Result, 0, len(checks))}
	for index, check := range checks {
		identity := acceptedCheckRuntime(fmt.Sprintf("check-run-%d", index))
		result := verification.Result{Name: check.Name, Command: append([]string(nil), check.Command...), Passed: true, ExitCode: &zero, Runtime: identity}
		if isQltyVerification(&result) {
			result.Output = `{"version":"2.1.0","runs":[{"results":[]}]}`
			artifact, err := f.engine.Store.CaptureResource(f.ctx, store.ArtifactOwner{Kind: "verification", ID: f.task.VerificationID}, "check:"+check.Name, "text/plain", []byte(result.Output), store.CaptureOptions{Limit: store.MessagePayloadLimit})
			if err != nil {
				t.Fatal(err)
			}
			result.OutputArtifact = artifact.ID
		}
		report.Results = append(report.Results, result)
		f.putRuntimeJob(t, check.Name, identity)
	}
	return report
}

func (f *acceptedReviewFixture) putRuntimeJob(t *testing.T, checkName string, identity verification.RuntimeIdentity) {
	t.Helper()
	job := runtimeCheckJob{Stage: "complete", ExecutionID: identity.ExecutionID, ExecutionGeneration: identity.Generation,
		LifecycleFile: identity.LifecycleFile, SandboxName: identity.SandboxName, SandboxID: identity.SandboxID,
		GatewayName: identity.GatewayName, GatewayEndpoint: identity.GatewayEndpoint, Workspace: identity.Workspace,
		CleanupVerified: true, TaskID: f.task.ID, VerificationID: f.task.VerificationID, Role: "verify", CheckID: checkName}
	if err := f.engine.Store.Put(f.ctx, "runtime_jobs", job.ExecutionID, job, 0); err != nil {
		t.Fatal(err)
	}
}

func acceptedCheckRuntime(executionID string) verification.RuntimeIdentity {
	return verification.RuntimeIdentity{ExecutionID: executionID, Generation: 1, Kind: "openshell", Launcher: "openshell",
		SandboxName: "whirlwind-check", SandboxID: "sandbox-1", GatewayName: "gateway", GatewayEndpoint: "unix:///tmp/gateway.sock",
		Workspace: "/workspace", LifecycleFile: "/tmp/lifecycle.json", CleanupVerified: true}
}

func (f *acceptedReviewFixture) saveReport(t *testing.T, report verification.Report, provenance model.Provenance) {
	t.Helper()
	f.evidence["report"], f.evidence["provenance"] = report, provenance
	if err := f.engine.Store.Put(f.ctx, "verification", f.task.VerificationID, f.evidence, f.version); err != nil {
		t.Fatal(err)
	}
	f.version++
}

func TestLoadAcceptedReviewReportRequiresCompleteEvidence(t *testing.T) {
	f := newAcceptedReviewFixture(t)
	f.saveReport(t, f.report, acceptedReviewProvenance(f.task, f.project.PolicyHash))
	if _, report, accepted, err := f.engine.LoadAcceptedReviewReport(f.ctx, f.task.ID); err != nil || !accepted || report.Score != 100 || report.Provenance.Head != acceptedCandidateRevision {
		t.Fatalf("accepted report missing or unbound: report=%+v accepted=%v err=%v", report, accepted, err)
	}

	missing := f.report
	missing.Results = nil
	f.assertRejected(t, missing, "passed=true without results")
	duplicate := append([]verification.Result(nil), f.report.Results...)
	duplicate = append(duplicate, f.report.Results[0])
	f.assertRejected(t, verification.Report{Passed: true, Results: duplicate}, "duplicate check result")
	noExit := append([]verification.Result(nil), f.report.Results...)
	noExit[0].ExitCode = nil
	f.assertRejected(t, verification.Report{Passed: true, Results: noExit}, "missing process exit")
	failed := append([]verification.Result(nil), f.report.Results...)
	code := 1
	failed[0].ExitCode, failed[0].Passed = &code, true
	f.assertRejected(t, verification.Report{Passed: true, Results: failed}, "failed process marked passed")
	wrongCommand := append([]verification.Result(nil), f.report.Results...)
	wrongCommand[0].Command = []string{"sh", "-c", "true"}
	f.assertRejected(t, verification.Report{Passed: true, Results: wrongCommand}, "mismatched check command")
	f.assertRejected(t, verification.Report{Passed: true, Results: f.report.Results[:len(f.report.Results)-1]}, "missing approved result")
	badRuntime := append([]verification.Result(nil), f.report.Results...)
	badRuntime[0].Runtime.CleanupVerified = false
	f.assertRejected(t, verification.Report{Passed: true, Results: badRuntime}, "missing cleanup proof")
	extra := verification.Result{Name: "supervisor-risk-check", Command: []string{"go", "test", "./..."}, Passed: true, ExitCode: intPointer(0), Runtime: acceptedCheckRuntime("extra-check")}
	f.putRuntimeJob(t, extra.Name, extra.Runtime)
	withExtra := append([]verification.Result(nil), f.report.Results...)
	withExtra = append(withExtra, extra)
	f.saveReport(t, verification.Report{Passed: true, Results: withExtra}, acceptedReviewProvenance(f.task, f.project.PolicyHash))
	if _, _, accepted, err := f.engine.LoadAcceptedReviewReport(f.ctx, f.task.ID); err != nil || !accepted {
		t.Fatalf("validated supervisor check invalidated mandatory plan inventory: accepted=%v err=%v", accepted, err)
	}
	badArtifact := append([]verification.Result(nil), f.report.Results...)
	for index := range badArtifact {
		if isQltyVerification(&badArtifact[index]) {
			badArtifact[index].OutputArtifact = "missing-artifact"
			break
		}
	}
	f.assertRejected(t, verification.Report{Passed: true, Results: badArtifact}, "missing full Qlty artifact")
	f.assertRejected(t, f.report, "stale candidate provenance", model.Provenance{Task: f.task.ID, PlanVersion: f.task.PlanVersion, Head: "stale-sha", Base: f.task.Base, PolicyHash: f.project.PolicyHash, VerificationID: f.task.VerificationID})
}

func TestLoadAcceptedReviewReportAcceptsUnchangedQltySmellsAdvisory(t *testing.T) {
	f := newAcceptedReviewFixture(t)
	index := qltyResultIndex(t, f.report)
	raw := f.report.Results[index]
	result := acceptedExitZeroQltyAdvisory(t, f, raw)
	f.report.Results[index] = result
	f.saveReport(t, f.report, acceptedReviewProvenance(f.task, f.project.PolicyHash))
	_, _, accepted, err := f.engine.LoadAcceptedReviewReport(f.ctx, f.task.ID)
	if err != nil || !accepted {
		t.Fatalf("unchanged exit-zero Qlty smells advisory rejected: accepted=%v err=%v", accepted, err)
	}
}

func qltyResultIndex(t *testing.T, report verification.Report) int {
	t.Helper()
	for index, result := range report.Results {
		if isQltyVerification(&result) {
			return index
		}
	}
	t.Fatal("fixture has no configured Qlty verification result")
	return -1
}

func acceptedExitZeroQltyAdvisory(t *testing.T, f *acceptedReviewFixture, raw verification.Result) verification.Result {
	t.Helper()
	raw.Output = unchangedQltySmellSARIF
	zero := 0
	raw.ExitCode, raw.Passed = &zero, false
	raw.Error = "qlty reported 1 code smells; repair findings before review"
	compared := verification.CompareBaseline(verification.Report{Results: []verification.Result{raw}}, verification.Report{Results: []verification.Result{raw}})
	if !compared.Passed || len(compared.Results) != 1 {
		t.Fatalf("baseline did not accept unchanged Qlty findings: %+v", compared)
	}
	result := compared.Results[0]
	artifact, err := f.engine.Store.CaptureResource(f.ctx, store.ArtifactOwner{Kind: "verification", ID: f.task.VerificationID}, "check:"+result.Name, "text/plain", []byte(result.Output), store.CaptureOptions{Limit: store.MessagePayloadLimit})
	if err != nil {
		t.Fatal(err)
	}
	result.OutputArtifact = artifact.ID
	persistRawRuntimeError(t, f, result.Runtime.ExecutionID, raw.Error)
	return result
}

func persistRawRuntimeError(t *testing.T, f *acceptedReviewFixture, executionID, rawError string) {
	t.Helper()
	record, err := f.engine.Store.Get(f.ctx, "runtime_jobs", executionID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[runtimeCheckJob](record)
	if err != nil {
		t.Fatal(err)
	}
	job.Error = rawError
	if err := f.engine.Store.Put(f.ctx, "runtime_jobs", executionID, job, record.Version); err != nil {
		t.Fatal(err)
	}
}

const unchangedQltySmellSARIF = `{"version":"2.1.0","runs":[{"results":[{"ruleId":"complexity","level":"warning","message":{"text":"Too complex"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"src/code.go"},"region":{"startLine":10,"snippet":{"text":"return largeFunction()"}}}}],"partialFingerprints":{"primaryLocationLineHash":"stable"},"properties":{"complexity":12}}]}]}`

func intPointer(value int) *int { return &value }

func (f *acceptedReviewFixture) assertRejected(t *testing.T, report verification.Report, name string, provenance ...model.Provenance) {
	t.Helper()
	proof := acceptedReviewProvenance(f.task, f.project.PolicyHash)
	if len(provenance) == 1 {
		proof = provenance[0]
	}
	f.saveReport(t, report, proof)
	if _, _, accepted, err := f.engine.LoadAcceptedReviewReport(f.ctx, f.task.ID); err == nil || accepted {
		t.Errorf("%s accepted: accepted=%v err=%v", name, accepted, err)
	}
}
