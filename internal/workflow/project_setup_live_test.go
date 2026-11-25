package workflow

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// Qlty and Gitleaks. Its distribution network grants are test-local.
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
	if err := os.WriteFile(filepath.Join(repository, "app.py"), []byte("def inspect(value):\n    return eval(value)\n"), 0600); err != nil {
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
	ctx := context.Background()
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
	currentID := setupID
	for phase := 0; phase < 3; phase++ {
		if err := e.runProjectSetup(ctx, currentID); err != nil {
			t.Fatalf("native setup phase %d: %v", phase+1, err)
		}
		record, err := e.Store.Get(ctx, "operations", currentID)
		requireWorkflowSuccess(t, err)
		operation, err := store.Decode[model.Operation](record)
		requireWorkflowSuccess(t, err)
		if operation.Stage != "complete" {
			evidence := setupEvidenceFromInput(operation.Input)
			t.Fatalf("native setup phase %d failed: %s results=%+v", phase+1, operation.Error, evidence.Report.Results)
		}
		if phase < 2 {
			operations, err := e.Store.List(ctx, "operations")
			requireWorkflowSuccess(t, err)
			currentID = ""
			for _, nextRecord := range operations {
				next, decodeErr := store.Decode[model.Operation](nextRecord)
				requireWorkflowSuccess(t, decodeErr)
				if nextRecord.ID != currentID && nextRecord.ID != setupID && next.Kind == "project_setup" && next.Subject == project.ID && next.Stage == "queued" {
					currentID = nextRecord.ID
					break
				}
			}
			if currentID == "" {
				t.Fatalf("native setup phase %d did not queue its durable child operation", phase+1)
			}
		}
	}
	finalRecord, err := e.Store.Get(ctx, "operations", currentID)
	requireWorkflowSuccess(t, err)
	finalOperation, err := store.Decode[model.Operation](finalRecord)
	requireWorkflowSuccess(t, err)
	finalEvidence := setupEvidenceFromInput(finalOperation.Input)
	identity := finalEvidence.Report.QualityIdentity
	if identity == nil || identity.Validate() != nil {
		t.Fatalf("native setup did not bind verified Qlty identity: %+v", identity)
	}
	for _, name := range []string{"gitleaks"} {
		found := false
		for _, tool := range identity.Tools {
			if tool.Name == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("selected scanner %s has no installed runtime identity: %+v", name, identity.Tools)
		}
	}
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
	if err := verification.ValidateJobSARIF([]byte(output)); err != nil {
		t.Fatalf("native scanner SARIF lacks successful complete invocation evidence: %v", err)
	}
	if !strings.Contains(output, "gitleaks:") || !strings.Contains(output, nativeSetupFixtureSensitiveFile) {
		t.Fatalf("native Gitleaks scanner did not report the harmless known canary: %s", output)
	}
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
	operations, err := e.Store.List(ctx, "operations")
	requireWorkflowSuccess(t, err)
	setupCount, ideationCount := 0, 0
	for _, item := range operations {
		operation, decodeErr := store.Decode[model.Operation](item)
		requireWorkflowSuccess(t, decodeErr)
		if operation.Subject != project.ID {
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
	t.Logf("native Qlty build %s; tools %v; effective catalog hash %s", identity.QltyVersion, identity.Tools, identity.PluginCatalogHash)
}
