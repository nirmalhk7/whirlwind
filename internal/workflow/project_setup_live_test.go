package workflow

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

const (
	nativeSetupFixtureSensitiveFile = "credentials.txt"
)

// This acceptance test exercises the registered-project path against installed
// Qlty and Gitleaks. Its distribution network grants are test-local. This
// qualifies onboarding for the unchanged HEAD, not changed-file detection.
func TestProjectSetupNativeAutomaticQltyOnboarding(t *testing.T) {
	if os.Getenv("WHIRLWIND_RUN_LIVE_QLTY_ONBOARDING") != "1" {
		t.Skip("set WHIRLWIND_RUN_LIVE_QLTY_ONBOARDING=1 to run native Qlty onboarding acceptance")
	}
	if _, err := exec.LookPath("qlty"); err != nil {
		t.Fatalf("installed Qlty required for native onboarding acceptance: %v", err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 required for source syntax check: %v", err)
	}
	e, _, repository := testEngine(t)
	installSetupRunner(t)
	qualityRoot := filepath.Join(t.TempDir(), "external-qlty")
	prepareNativeSetupRepository(t, repository, python, qualityRoot)
	ctx := context.Background()
	project, setupID := registerNativeSetupProject(t, ctx, e, repository)
	finalID := runNativeOnboardingPhases(t, ctx, e, project.ID, setupID)
	finalEvidence := nativeSetupIdentityEvidence(t, ctx, e, finalID)
	assertNativeSetupScannerStreams(t, finalEvidence)
	identity := finalEvidence.Report.QualityIdentity
	assertNativeSetupReadiness(t, ctx, e, project, repository, identity)
	assertNativeSetupOperationCounts(t, ctx, e, project.ID)
	t.Logf("native Qlty build %s; tools %v; effective catalog hash %s", identity.QltyVersion, identity.Tools, identity.PluginCatalogHash)
}

func prepareNativeSetupRepository(t *testing.T, repository, python, qualityRoot string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, "app.py"), []byte("def inspect(value):\n    return value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Generate a synthetic scanner canary only in the disposable repository.
	// No credential-shaped value belongs in the tracked test source.
	seed := sha256.Sum256([]byte("whirlwind native onboarding scanner canary"))
	canary := fmt.Sprintf("token = \"ghp_%x\"\n", seed[:18])
	if err := os.WriteFile(filepath.Join(repository, nativeSetupFixtureSensitiveFile), []byte(canary), 0600); err != nil {
		t.Fatal(err)
	}
	projectPolicy := model.Policy{
		Quality:      &model.QualityPolicy{Root: qualityRoot},
		Checks:       []model.Check{{Name: "python-syntax", Command: []string{python, "-c", "import ast,pathlib; ast.parse(pathlib.Path('app.py').read_text())"}, TimeoutSeconds: 60}},
		AllowedPaths: []string{"**"},
		RiskRules:    []model.RiskRule{{Level: "low", Paths: []string{"**"}}},
		Sandbox:      &model.SandboxPolicy{AllowedDomains: []string{"github.com", "api.github.com", "release-assets.githubusercontent.com"}},
	}
	encodedPolicy, err := yaml.Marshal(projectPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".whirlwind.yaml"), encodedPolicy, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "app.py", nativeSetupFixtureSensitiveFile, ".whirlwind.yaml"}, {"-c", "core.hooksPath=/dev/null", "commit", "-m", "Prepare native quality onboarding fixture"}} {
		command := exec.Command("git", append([]string{"-C", repository}, args...)...)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Onboarding", "GIT_AUTHOR_EMAIL=onboarding@localhost", "GIT_COMMITTER_NAME=Onboarding", "GIT_COMMITTER_EMAIL=onboarding@localhost")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("commit native onboarding fixture: %v %s", err, output)
		}
	}
}

func registerNativeSetupProject(t *testing.T, ctx context.Context, e *Engine, repository string) (model.Project, string) {
	t.Helper()
	project, err := e.Project(ctx, "p")
	requireWorkflowSuccess(t, err)
	project.Policy, project.PolicyHash, err = policy.Load(repository)
	requireWorkflowSuccess(t, err)
	project.Status = "draft"
	projectRecord, err := e.Store.Get(ctx, "projects", project.ID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.Store.Put(ctx, "projects", project.ID, project, projectRecord.Version))
	changes := store.InitialProjectWork(project.ID, time.Now())
	setupID := ""
	for _, change := range changes {
		if change.Kind == "operations" {
			if operation, ok := change.Value.(model.Operation); ok && operation.Kind == "project_setup" {
				setupID = change.ID
			}
		}
	}
	if setupID == "" {
		t.Fatal("registration did not create a durable setup operation")
	}
	requireWorkflowSuccess(t, e.Store.Write(ctx, changes, nil))
	return project, setupID
}

func runNativeOnboardingPhases(t *testing.T, ctx context.Context, e *Engine, projectID, setupID string) string {
	t.Helper()
	currentID := setupID
	for phase := 1; phase <= 3; phase++ {
		requireCompletedNativeSetupPhase(t, ctx, e, currentID, phase)
		if phase < 3 {
			currentID = queuedNativeSetupChild(t, ctx, e, projectID, setupID, phase)
		}
	}
	return currentID
}

func requireCompletedNativeSetupPhase(t *testing.T, ctx context.Context, e *Engine, operationID string, phase int) {
	t.Helper()
	if err := e.runProjectSetup(ctx, operationID); err != nil {
		t.Fatalf("native setup phase %d: %v", phase, err)
	}
	record, err := e.Store.Get(ctx, "operations", operationID)
	requireWorkflowSuccess(t, err)
	operation, err := store.Decode[model.Operation](record)
	requireWorkflowSuccess(t, err)
	if operation.Stage != "complete" {
		evidence := setupEvidenceFromInput(operation.Input)
		t.Fatalf("native setup phase %d failed: %s results=%+v", phase, operation.Error, evidence.Report.Results)
	}
}

func queuedNativeSetupChild(t *testing.T, ctx context.Context, e *Engine, projectID, setupID string, phase int) string {
	t.Helper()
	operations, err := e.Store.List(ctx, "operations")
	requireWorkflowSuccess(t, err)
	for _, record := range operations {
		next, err := store.Decode[model.Operation](record)
		requireWorkflowSuccess(t, err)
		if record.ID != "" && record.ID != setupID && next.Kind == "project_setup" && next.Subject == projectID && next.Stage == "queued" {
			return record.ID
		}
	}
	t.Fatalf("native setup phase %d did not queue its durable child operation", phase)
	return ""
}

func nativeSetupIdentityEvidence(t *testing.T, ctx context.Context, e *Engine, operationID string) projectSetupEvidence {
	t.Helper()
	finalRecord, err := e.Store.Get(ctx, "operations", operationID)
	requireWorkflowSuccess(t, err)
	finalOperation, err := store.Decode[model.Operation](finalRecord)
	requireWorkflowSuccess(t, err)
	finalEvidence := setupEvidenceFromInput(finalOperation.Input)
	identity := finalEvidence.Report.QualityIdentity
	if identity == nil || identity.Validate() != nil {
		t.Fatalf("native setup did not bind verified Qlty identity: %+v", identity)
	}
	if !slices.ContainsFunc(identity.Tools, func(tool model.QualityToolIdentity) bool {
		return tool.Name == "gitleaks"
	}) {
		t.Fatalf("selected scanner gitleaks has no installed runtime identity: %+v", identity.Tools)
	}
	return finalEvidence
}

func assertNativeSetupScannerStreams(t *testing.T, finalEvidence projectSetupEvidence) {
	t.Helper()
	qltyIndex := -1
	for index, command := range finalEvidence.Manifest.Commands {
		if command.Name == "qlty-qualification" {
			qltyIndex = index
		}
	}
	if qltyIndex < 0 {
		t.Fatal("final setup operation omitted required Qlty qualification")
	}
	syntaxVerified := false
	for _, result := range finalEvidence.Report.Results {
		syntaxVerified = syntaxVerified || result.Name == "python-syntax" && result.Status == "pass" && result.Runtime.CleanupVerified
	}
	if !syntaxVerified {
		t.Fatal("required Python syntax check lacks successful native execution evidence")
	}
	output, _, err := verification.ReadJobStreams(finalEvidence.Manifest.Root, qltyIndex)
	if err != nil {
		t.Fatalf("native scanner SARIF streams were not retained: %v", err)
	}
	// The separate changed-candidate fixture requires a Gitleaks finding.
	if err := verifyQltyOnboardingBaselineEvidence(finalEvidence.Manifest, qltyIndex, []byte(output)); err != nil {
		t.Fatal(err)
	}
}

func assertNativeSetupReadiness(t *testing.T, ctx context.Context, e *Engine, project model.Project, repository string, identity *model.QualityIdentity) {
	t.Helper()
	savedProject, err := e.Project(ctx, project.ID)
	requireWorkflowSuccess(t, err)
	if savedProject.Status != "approved" || savedProject.QualityIdentity == nil || !model.SameQualityIdentity(savedProject.QualityIdentity, identity) {
		t.Fatalf("project readiness did not follow verified identity: %+v", savedProject)
	}
	if _, err := os.Lstat(filepath.Join(repository, ".qlty")); !os.IsNotExist(err) {
		t.Fatalf("Qlty polluted the source repository: %v", err)
	}
	if changes, err := exec.Command("git", "-C", repository, "status", "--porcelain").Output(); err != nil || len(changes) != 0 {
		t.Fatalf("native onboarding changed source tree: %q (%v)", changes, err)
	}
}

func assertNativeSetupOperationCounts(t *testing.T, ctx context.Context, e *Engine, projectID string) {
	t.Helper()
	operations, err := e.Store.List(ctx, "operations")
	requireWorkflowSuccess(t, err)
	setupCount, ideationCount := 0, 0
	for _, item := range operations {
		operation, decodeErr := store.Decode[model.Operation](item)
		requireWorkflowSuccess(t, decodeErr)
		if operation.Subject != projectID {
			continue
		}
		if operation.Kind == "project_setup" {
			setupCount++
		}
		if operation.Kind == "ideation" {
			ideationCount++
		}
	}
	if setupCount != 3 || ideationCount != 1 {
		t.Fatalf("native onboarding duplicated or lost work: setup=%d ideation=%d", setupCount, ideationCount)
	}

}

// A valid empty SARIF result is legitimate for an unchanged baseline, but it
// cannot be counted as changed-candidate detection evidence.
func verifyQltyOnboardingBaselineEvidence(manifest verification.JobManifest, index int, output []byte) error {
	if manifest.Purpose != verification.ProjectSetupPurpose || manifest.BaseCommit == "" || manifest.BaseCommit != manifest.CandidateCommit {
		return errors.New("onboarding qualification must compare the registered commit against itself")
	}
	argv := []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"}
	if index < 0 || index >= len(manifest.Commands) || !slices.Equal(manifest.Commands[index].Command, argv) ||
		manifest.Commands[index].Mode != "autofix" || manifest.Commands[index].ReportFormat != "sarif" {
		return errors.New("onboarding qualification changed the approved HEAD-diff check")
	}
	if err := verification.ValidateJobSARIF(output); err != nil {
		return fmt.Errorf("onboarding qualification lacks complete scanner evidence: %w", err)
	}
	return nil
}

func TestQltyOnboardingBaselineEvidenceIsNotCandidateFindingCoverage(t *testing.T) {
	manifest := verification.JobManifest{Purpose: verification.ProjectSetupPurpose, BaseCommit: "onboarded-head",
		CandidateCommit: "onboarded-head", Commands: []verification.JobCommand{{Name: "qlty-qualification",
			Command: []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"},
			Mode:    "autofix", ReportFormat: "sarif"}}}
	clean := []byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"invocations":[{"executionSuccessful":true}],"results":[]}]}`)
	if err := verifyQltyOnboardingBaselineEvidence(manifest, 0, clean); err != nil {
		t.Fatalf("valid empty report may qualify the unchanged baseline: %v", err)
	}
	for _, bad := range [][]byte{
		[]byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"invocations":[{"executionSuccessful":false}],"results":[]}]}`),
		[]byte(`{}`),
	} {
		if err := verifyQltyOnboardingBaselineEvidence(manifest, 0, bad); err == nil {
			t.Fatalf("failed or incomplete scanner report qualified the baseline: %s", bad)
		}
	}
	manifest.CandidateCommit = "changed-candidate"
	if err := verifyQltyOnboardingBaselineEvidence(manifest, 0, clean); err == nil {
		t.Fatal("candidate report was accepted as unchanged onboarding evidence")
	}
	manifest.CandidateCommit = manifest.BaseCommit
	manifest.Commands[0].Command[2] = "--all"
	if err := verifyQltyOnboardingBaselineEvidence(manifest, 0, clean); err == nil {
		t.Fatal("changed command was accepted as approved HEAD-diff evidence")
	}
}
