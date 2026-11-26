package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/verification"
)

const (
	runnerNativeAcceptanceLabel   = "native macOS verification acceptance"
	runnerFixtureBinaryName       = "whirlwind-verify"
	runnerFixtureBuildPath        = "./cmd/whirlwind-verify"
	runnerFixtureBuildErrorFormat = "build runner: %v %s"
	runnerFixtureLauncherName     = "launch.mjs"
	runnerFixtureRevParseArg      = "rev-parse"
	runnerFixtureApprovedHash     = "approved-bytes"
	runnerFixtureTrueBinary       = "/usr/bin/true"
	runnerFixtureManifestFilename = "manifest.json"
	runnerFixtureResultPath       = "result.txt"
	runnerFixtureReviewerPurpose  = "review-broker"
	runnerFixtureNewFileContent   = "new file"
)

func TestParityCanonicalVerificationUsesIndependentRunner(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip(runnerNativeAcceptanceLabel)
	}
	e, plan, _ := testEngine(t)
	ctx := context.Background()
	_, source, _, _ := runtime.Caller(0)
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	binary := filepath.Join(t.TempDir(), runnerFixtureBinaryName)
	build := exec.Command("go", "build", "-o", binary, runnerFixtureBuildPath)
	build.Dir = repo
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatalf(runnerFixtureBuildErrorFormat, err, raw)
	}
	t.Setenv("WHIRLWIND_VERIFY_BIN", binary)
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", filepath.Join(repo, "bridges", "nono", runnerFixtureLauncherName))
	project, err := e.Project(ctx, plan.ProjectID)
	requireWorkflowSuccess(t, err)
	plan.IdeaID, plan.Worktree = "idea", project.Path
	plan.Base, err = github.Run(ctx, project.Path, "git", runnerFixtureRevParseArg, "HEAD")
	requireWorkflowSuccess(t, err)
	plan.PlanDocument = &model.PlanDocument{Hash: runnerFixtureApprovedHash, ApprovedHash: runnerFixtureApprovedHash, PolicyHash: project.PolicyHash}
	frozen := model.PlanSpecification{Approval: &model.PlanApproval{DocumentHash: runnerFixtureApprovedHash, PlanVersion: plan.PlanVersion, ConfigurationHash: "frozen-verification-config"}}
	requireWorkflowSuccess(t, e.Store.Put(ctx, "plan_versions", fmt.Sprintf("%s:%d", plan.ID, plan.PlanVersion), frozen, 0))
	plan.Plan = &frozen
	run, err := e.startVerification(ctx, project, &plan)
	requireWorkflowSuccess(t, err)
	observed := 0
	report, err := run.runChecks(project.Policy, []model.Check{{Name: "test", Command: []string{runnerFixtureTrueBinary}, TimeoutSeconds: 5}}, func(verification.Result) error { observed++; return nil })
	if err != nil || !report.Passed || observed != 1 {
		t.Fatalf("verification failed: %+v %v observed=%d", report, err, observed)
	}
	if _, err := os.Stat(filepath.Join(e.Root, "verification", run.id, runnerFixtureManifestFilename)); err != nil {
		t.Fatalf("canonical verification bypassed runner manifest/journal: %v", err)
	}
	if _, ok := run.value["runner_manifest"]; !ok {
		t.Fatal("supervisor did not persist frozen runner binding before launch")
	}
	if _, ok := run.value["runner_report"]; !ok {
		t.Fatal("supervisor lost complete runner report")
	}
	// Simulate supervisor loss after runner completion but before workflow handoff.
	// The durable journal must stay attached to the same verification identity.
	requireWorkflowSuccess(t, e.Store.RecoverInterrupted(ctx, "replacement-owner"))
	replacement := New(e.Store, e.GitHub, e.Root)
	plan.VerificationID = "" // the execution-plan write may also have been lost
	recovered, err := replacement.startVerification(ctx, project, &plan)
	requireWorkflowSuccess(t, err)
	if recovered.id != run.id {
		t.Fatalf("restart discarded durable verifier journal: old=%s new=%s", run.id, recovered.id)
	}
	replayed, err := recovered.runChecks(project.Policy, []model.Check{{Name: "test", Command: []string{runnerFixtureTrueBinary}, TimeoutSeconds: 5}}, nil)
	if err != nil || !replayed.Passed || len(replayed.Results) != 1 || replayed.Results[0].StartedAt != report.Results[0].StartedAt {
		t.Fatalf("restart replayed completed command: %+v %v", replayed, err)
	}
	plan.Plan = nil // unit requests recover the immutable approved version
	baseline, err := replacement.qualityBaseline(ctx, project, plan, []model.Check{{Name: "baseline", Command: []string{runnerFixtureTrueBinary}, TimeoutSeconds: 5}})
	if err != nil || !baseline.Passed {
		t.Fatalf("baseline failed: %+v %v", baseline, err)
	}
	assertIndependentBaselineManifest(t, e.Root, run.id, plan.Base)
}

func TestParityRunnerPreservesApprovedCommandDependencies(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip(runnerNativeAcceptanceLabel)
	}
	e, plan, _ := testEngine(t)
	ctx := context.Background()
	_, source, _, _ := runtime.Caller(0)
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	binary := filepath.Join(t.TempDir(), runnerFixtureBinaryName)
	build := exec.Command("go", "build", "-o", binary, runnerFixtureBuildPath)
	build.Dir = repo
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatalf(runnerFixtureBuildErrorFormat, err, raw)
	}
	t.Setenv("WHIRLWIND_VERIFY_BIN", binary)
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", filepath.Join(repo, "bridges", "nono", runnerFixtureLauncherName))
	project, err := e.Project(ctx, plan.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	plan.IdeaID, plan.Worktree = "idea", project.Path
	plan.Base, err = github.Run(ctx, project.Path, "git", runnerFixtureRevParseArg, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	plan.PlanDocument = &model.PlanDocument{Hash: runnerFixtureApprovedHash, ApprovedHash: runnerFixtureApprovedHash, PolicyHash: project.PolicyHash}
	run, err := e.startVerification(ctx, project, &plan)
	if err != nil {
		t.Fatal(err)
	}
	var checks []model.Check
	if err := json.Unmarshal([]byte(`[{"name":"failure","command":["/usr/bin/false"],"timeout_seconds":5},{"name":"blocked","command":["/usr/bin/true"],"timeout_seconds":5,"depends_on":["failure"]}]`), &checks); err != nil {
		t.Fatal(err)
	}
	report, err := run.runChecks(project.Policy, checks, nil)
	if err != nil || len(report.Results) != 2 || report.Results[1].Passed || !strings.HasPrefix(report.Results[1].Error, "dependency ") {
		t.Fatalf("approved dependency lost before runner: %+v %v", report, err)
	}
}

func TestParityMissingRunnerPausesInsteadOfEnteringCorrectionLoop(t *testing.T) {
	e, plan, _ := testEngine(t)
	ctx := context.Background()
	project, err := e.Project(ctx, plan.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	plan.IdeaID, plan.Worktree = "idea", project.Path
	plan.Base, err = github.Run(ctx, project.Path, "git", runnerFixtureRevParseArg, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	plan.PlanDocument = &model.PlanDocument{Hash: "approved", ApprovedHash: "approved", PolicyHash: project.PolicyHash}
	t.Setenv("WHIRLWIND_VERIFY_BIN", filepath.Join(t.TempDir(), "missing-runner"))
	run, err := e.startVerification(ctx, project, &plan)
	if err != nil {
		t.Fatal(err)
	}
	_, failure := run.runChecks(project.Policy, []model.Check{{Name: "test", Command: []string{runnerFixtureTrueBinary}, TimeoutSeconds: 5}}, nil)
	if failure == nil {
		t.Fatal("missing runner passed")
	}
	plan.IdeaID = ""
	plan.Stage = "review"
	record, err := e.Store.Get(ctx, "execution_plans", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	step := taskStep{Engine: e, ctx: ctx, project: project, task: plan, record: record}
	_, resultErr := step.failedReviewVerification(failure)
	if resultErr == nil || step.task.Stage == "repair" {
		t.Fatalf("missing tools triggered Coding repair instead of actionable pause: stage=%s err=%v", step.task.Stage, resultErr)
	}
}

func TestParityCodingCheckFreezesDirtySourceThroughIndependentRunner(t *testing.T) {
	e, plan := approvedCodingPairFixture(t)
	ctx := context.Background()
	installNativeParityVerifier(t)
	project, err := e.Project(ctx, plan.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	plan.Worktree = project.Path
	plan.Base, err = github.Run(ctx, project.Path, "git", runnerFixtureRevParseArg, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project.Path, runnerFixtureResultPath), []byte("uncommitted result\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := github.Run(ctx, project.Path, "git", "status", "--porcelain", "-uall")
	if err != nil {
		t.Fatal(err)
	}
	indexBefore, err := github.Run(ctx, project.Path, "git", "ls-files", "--stage")
	if err != nil {
		t.Fatal(err)
	}
	attempt := &agentAttempt{agentRun: &agentRun{Engine: e, ctx: ctx, project: project, task: plan, input: provider.Input{Role: "code", CWD: project.Path}}, id: "coding-broker"}
	run := codingCheckRun{attempt: attempt, check: project.Policy.Checks[0]}
	if err := run.prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := run.execute(ctx); err != nil {
		t.Fatal(err)
	}
	if !run.result.Passed {
		t.Fatalf("uncommitted source omitted from check snapshot: %+v", run.result)
	}
	manifest := onlyBrokerManifest(t, e.Root)
	if manifest.CandidateCommit == plan.Base || manifest.PlanHash != plan.PlanDocument.ApprovedHash || manifest.ConfigurationHash != verificationConfigurationHash(plan) {
		t.Fatalf("dirty source not frozen with approval identity: %+v", manifest)
	}
	after, _ := github.Run(ctx, project.Path, "git", "status", "--porcelain", "-uall")
	indexAfter, _ := github.Run(ctx, project.Path, "git", "ls-files", "--stage")
	if before != after || indexBefore != indexAfter {
		t.Fatal("freezing broker snapshot modified original files or index")
	}
}

func TestParityReviewerCheckUsesIndependentRunnerForReviewedCommit(t *testing.T) {
	e, plan := approvedCodingPairFixture(t)
	ctx := context.Background()
	installNativeParityVerifier(t)
	project, err := e.Project(ctx, plan.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	plan.Worktree = project.Path
	plan.Base, err = github.Run(ctx, project.Path, "git", runnerFixtureRevParseArg, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project.Path, runnerFixtureResultPath), []byte("committed result\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := github.Run(ctx, project.Path, "git", "add", runnerFixtureResultPath); err != nil {
		t.Fatal(err)
	}
	if _, err := github.Run(ctx, project.Path, "git", "commit", "-m", "review candidate"); err != nil {
		t.Fatal(err)
	}
	head, err := github.Run(ctx, project.Path, "git", runnerFixtureRevParseArg, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(project.Path, runnerFixtureResultPath)); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "sessions", runnerFixtureReviewerPurpose, model.Attempt{AttemptID: runnerFixtureReviewerPurpose, Task: plan.ID, ProjectID: project.ID, PlanVersion: plan.PlanVersion, Role: "review", Finished: true}, 0); err != nil {
		t.Fatal(err)
	}
	attempt := &agentAttempt{agentRun: &agentRun{Engine: e, ctx: ctx, project: project, task: plan, input: provider.Input{Role: "review", ReadOnly: true, CWD: project.Path}}, id: runnerFixtureReviewerPurpose}
	result, err := attempt.executeReviewerCheck(ctx, "test", project.Policy.Checks[0], project, head)
	if err != nil || !result.Passed {
		t.Fatalf("review check did not verify immutable commit: %+v %v", result, err)
	}
	manifest := onlyBrokerManifest(t, e.Root)
	if manifest.CandidateCommit != head || manifest.PlanHash != plan.PlanDocument.ApprovedHash {
		t.Fatalf("review check manifest does not match reviewed commit: %+v", manifest)
	}
	if _, err := os.Stat(filepath.Join(project.Path, runnerFixtureResultPath)); !os.IsNotExist(err) {
		t.Fatal("Reviewer check changed live source")
	}
}

func installNativeParityVerifier(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip(runnerNativeAcceptanceLabel)
	}
	_, source, _, _ := runtime.Caller(0)
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	binary := filepath.Join(t.TempDir(), runnerFixtureBinaryName)
	build := exec.Command("go", "build", "-o", binary, runnerFixtureBuildPath)
	build.Dir = repo
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatalf(runnerFixtureBuildErrorFormat, err, raw)
	}
	t.Setenv("WHIRLWIND_VERIFY_BIN", binary)
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", filepath.Join(repo, "bridges", "nono", runnerFixtureLauncherName))
}

func onlyBrokerManifest(t *testing.T, root string) verification.JobManifest {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "verification"))
	if err != nil {
		t.Fatalf("broker bypassed independent verifier: %v", err)
	}
	var found []verification.JobManifest
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(root, "verification", entry.Name(), runnerFixtureManifestFilename))
		if err != nil {
			continue
		}
		var manifest verification.JobManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		found = append(found, manifest)
	}
	if len(found) != 1 {
		t.Fatalf("expected one durable broker runner manifest, got %d", len(found))
	}
	return found[0]
}

func TestParityCodingCheckSnapshotPreservesDirectoryToFileReplacement(t *testing.T) {
	e, plan, repo := testEngine(t)
	ctx := context.Background()
	if err := os.Mkdir(filepath.Join(repo, "replacement"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "replacement", "old.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := github.Run(ctx, repo, "git", "add", "replacement"); err != nil {
		t.Fatal(err)
	}
	if _, err := github.Run(ctx, repo, "git", "commit", "-m", "replacement fixture"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(repo, "replacement")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "replacement"), []byte(runnerFixtureNewFileContent), 0600); err != nil {
		t.Fatal(err)
	}
	fingerprint, _, _, files, err := codingSnapshotManifest(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	a := &agentAttempt{agentRun: &agentRun{Engine: e, ctx: ctx, task: plan, input: provider.Input{CWD: repo}}}
	root := t.TempDir()
	target := filepath.Join(root, "frozen")
	if err := a.freezeCodingCheck(ctx, root, target, fingerprint, files); err != nil {
		t.Fatalf("valid directory-to-file change could not be frozen: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(target, "replacement"))
	if err != nil || string(raw) != runnerFixtureNewFileContent {
		t.Fatalf("wrong frozen source: %q %v", raw, err)
	}
}

func TestParityCodingCheckSnapshotPreservesFileToDirectoryReplacement(t *testing.T) {
	e, plan, repo := testEngine(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(repo, "replacement"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := github.Run(ctx, repo, "git", "add", "replacement"); err != nil {
		t.Fatal(err)
	}
	if _, err := github.Run(ctx, repo, "git", "commit", "-m", "replacement fixture"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "replacement")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, "replacement"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "replacement", "new.txt"), []byte(runnerFixtureNewFileContent), 0600); err != nil {
		t.Fatal(err)
	}
	fingerprint, _, _, files, err := codingSnapshotManifest(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	a := &agentAttempt{agentRun: &agentRun{Engine: e, ctx: ctx, task: plan, input: provider.Input{CWD: repo}}}
	root := t.TempDir()
	target := filepath.Join(root, "frozen")
	if err := a.freezeCodingCheck(ctx, root, target, fingerprint, files); err != nil {
		t.Fatalf("valid file-to-directory change could not be frozen: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(target, "replacement", "new.txt"))
	if err != nil || string(raw) != runnerFixtureNewFileContent {
		t.Fatalf("wrong frozen source: %q %v", raw, err)
	}
}

func TestParityBrokerCheckRunsApprovedDependenciesInOrder(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := context.Background()
	project, err := e.Project(ctx, idea.ProjectID)
	requireWorkflowSuccess(t, err)
	project.Policy.Checks[0].DependsOn = []string{"setup"}
	project.Policy.Checks = append([]model.Check{{Name: "setup", Command: []string{runnerFixtureTrueBinary}, TimeoutSeconds: 5}}, project.Policy.Checks...)
	raw, err := yaml.Marshal(project.Policy)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, os.WriteFile(filepath.Join(project.Path, ".whirlwind.yaml"), raw, 0600))
	if _, err := github.Run(ctx, project.Path, "git", "add", ".whirlwind.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := github.Run(ctx, project.Path, "git", "commit", "-m", "human configured dependencies"); err != nil {
		t.Fatal(err)
	}
	project.Policy, project.PolicyHash, err = policy.Load(project.Path)
	requireWorkflowSuccess(t, err)
	row, err := e.Store.Get(ctx, "projects", project.ID)
	requireWorkflowSuccess(t, err)
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
	plan.Base, err = github.Run(ctx, project.Path, "git", runnerFixtureRevParseArg, "HEAD")
	requireWorkflowSuccess(t, err)
	a := &agentAttempt{agentRun: &agentRun{Engine: e, ctx: ctx, project: project, task: plan, input: provider.Input{Role: "code", CWD: project.Path}}, id: "dependency-broker"}
	result, err := a.runIndependentBrokerCheck(ctx, project.Path, plan.Base, project.Policy.Checks[1])
	if err != nil {
		t.Fatalf("approved dependency chain did not execute: %v", err)
	}
	if result.Name != "test" || result.ExitCode == nil || *result.ExitCode != 1 || result.Passed {
		t.Fatalf("root command result lost: %+v", result)
	}
	manifest := onlyBrokerManifest(t, e.Root)
	if len(manifest.Commands) != 2 || manifest.Commands[0].Name != "setup" || manifest.Commands[1].Name != "test" {
		t.Fatalf("dependency order not preserved: %+v", manifest.Commands)
	}

	// Unit agents carry no mutable PlanSpecification. Baseline and candidate
	// broker jobs must both recover the immutable approved configuration.
	a.task.Plan = nil
	requireWorkflowSuccess(t, e.Store.Put(ctx, "sessions", a.id, model.Attempt{AttemptID: a.id, Task: plan.ID, ProjectID: project.ID, PlanVersion: plan.PlanVersion, Role: "code", Finished: true}, 0))
	requireWorkflowSuccess(t, e.recordCodingOrigin(ctx, a.task, project.Path, ""))
	coding := codingCheckRun{attempt: a, check: project.Policy.Checks[1]}
	requireWorkflowSuccess(t, coding.prepare(ctx))
	requireWorkflowSuccess(t, coding.execute(ctx))
	if err := coding.persist(ctx); err != nil {
		t.Fatalf("dependency-aware Coding baseline failed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(e.Root, "verification"))
	requireWorkflowSuccess(t, err)
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(e.Root, "verification", entry.Name(), runnerFixtureManifestFilename))
		requireWorkflowSuccess(t, err)
		var recorded verification.JobManifest
		requireWorkflowSuccess(t, json.Unmarshal(raw, &recorded))
		if recorded.ConfigurationHash != verificationConfigurationHash(plan) {
			t.Fatal("broker/baseline lost frozen approval configuration when unit document omitted")
		}
	}
}

func assertIndependentBaselineManifest(t *testing.T, root, runID, base string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "verification"))
	requireWorkflowSuccess(t, err)
	foundBaseline := false
	for _, entry := range entries {
		if entry.Name() == runID {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, "verification", entry.Name(), runnerFixtureManifestFilename))
		if err != nil {
			continue
		}
		var manifest verification.JobManifest
		if json.Unmarshal(body, &manifest) == nil && len(manifest.Commands) == 1 && manifest.Commands[0].Name == "baseline" && manifest.CandidateCommit == base && manifest.ConfigurationHash == "frozen-verification-config" {
			foundBaseline = true
		}
	}
	if !foundBaseline {
		t.Fatal("baseline bypassed independent verification subprocess")
	}
}
