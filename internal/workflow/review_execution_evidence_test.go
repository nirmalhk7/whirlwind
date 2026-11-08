package workflow

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
	"whirlwind/prompts/agents"
)

func TestReviewEvidenceProjectionFitsDefaultContextAndRetainsHostProof(t *testing.T) {
	engine, task, repo := testEngine(t)
	ctx := context.Background()
	project, err := engine.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	writeReviewEvidenceFixture(t, repo)

	plan := model.Plan{
		Objective:                "Return useful result",
		What:                     "Return true from Result with regression coverage",
		Why:                      "Consumers currently receive false",
		Benefit:                  "Consumers receive a usable result",
		Where:                    []string{"result.go", "result_test.go"},
		AcceptanceCriteria:       []string{"Result returns true", "Regression test fails before the change and passes afterwards"},
		Scope:                    []string{"Change Result and add a regression test"},
		Exclusions:               []string{"Do not change unrelated tests"},
		Steps:                    []string{"Add the failing regression", "Implement Result", "Run the approved test"},
		RepositoryTestCommands:   [][]string{{"go", "test", "-json", "./..."}},
		RepositoryTestInventory:  "Configured: .whirlwind.yaml: go test -json ./...",
		RepositoryBuildInventory: "None configured: .whirlwind.yaml inspected",
		Requirements:             []model.Requirement{{ID: "REQ-RESULT", Outcome: "Result returns true", EvidenceMethod: "Run TestResult"}},
		Units:                    []model.Unit{{ID: "result", Objective: "Test first, implement Result, verify regression", Dependencies: []string{}, AllowedPaths: []string{"result.go", "result_test.go"}}},
	}
	planDocument := RenderPlanDocument(plan)
	plan, err = ParsePlanDocument(planDocument)
	if err != nil {
		t.Fatal(err)
	}
	task.Plan = &plan
	task.PlanVersion = 1
	task.Worktree = repo
	task.VerificationID = "verification-review-context"
	task.Issue = model.Issue{Number: 3, Title: "Return true", Body: "Result must return true", State: "OPEN", Labels: []string{"whirlwind:ready"}, URL: "https://issues.example/APP-3"}
	step := &taskStep{Engine: engine, ctx: ctx, project: project, task: task}

	baseline := verification.Report{Passed: true, Results: []verification.Result{
		reviewEvidenceResult("test", []string{"go", "test", "-json", "./..."}, `{"Action":"pass"}`, ""),
		reviewEvidenceResult("plan-test-1", []string{"go", "test", "-json", "./..."}, `{"Action":"pass"}`, ""),
		reviewEvidenceResult("qlty-smells", []string{"qlty", "smells", "--all", "--sarif"}, reviewSarif(5), ""),
		reviewEvidenceResult("qlty-check", []string{"qlty", "check", "--all", "--sarif"}, reviewSarif(7948), ""),
	}}
	current := verification.Report{Passed: true, Results: []verification.Result{
		reviewEvidenceResult("test", []string{"go", "test", "-json", "./..."}, strings.Repeat(`{"Action":"pass","Output":"fixture"}`+"\n", 28), "test-output-artifact"),
		reviewEvidenceResult("plan-test-1", []string{"go", "test", "-json", "./..."}, strings.Repeat(`{"Action":"pass","Output":"fixture"}`+"\n", 28), "plan-test-output-artifact"),
		reviewEvidenceResult("qlty-smells", []string{"qlty", "smells", "--all", "--sarif"}, reviewSarif(496), "smells-output-artifact"),
		reviewEvidenceResult("qlty-check", []string{"qlty", "check", "--all", "--sarif"}, reviewSarif(4096), "qlty-output-artifact"),
		reviewEvidenceResult("diff", []string{"git", "diff", "--check", "base...HEAD"}, "", "diff-output-artifact"),
		reviewEvidenceResult("tracked-files-clean", []string{"git", "diff", "--exit-code"}, "", "tracked-output-artifact"),
		reviewEvidenceResult("index-clean", []string{"git", "diff", "--cached", "--exit-code"}, "", "index-output-artifact"),
	}}
	verificationRecord := struct {
		Provenance model.Provenance    `json:"provenance"`
		Stage      string              `json:"stage"`
		Report     verification.Report `json:"report"`
		Baseline   verification.Report `json:"baseline"`
	}{
		Provenance: model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Head: "candidate-head", Base: "pinned-base", PolicyHash: project.PolicyHash, VerificationID: task.VerificationID},
		Stage:      "complete",
		Report:     current,
		Baseline:   baseline,
	}
	if err := engine.Store.Put(ctx, "verification", task.VerificationID, verificationRecord, 0); err != nil {
		t.Fatal(err)
	}
	storedBefore, err := engine.Store.Get(ctx, "verification", task.VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	redArtifact, greenArtifact := saveReviewTDDArtifacts(t, engine.Store, ctx)
	_, redContentBefore, err := engine.Store.Artifact(ctx, redArtifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, greenContentBefore, err := engine.Store.Artifact(ctx, greenArtifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	redSnapshot, redProduction, redTests, redFiles, err := codingSnapshotManifest(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "result.go"), []byte("package result\n\nfunc Result() bool {\n\treturn true\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	greenSnapshot, greenProduction, greenTests, greenFiles, err := codingSnapshotManifest(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if redTests != greenTests {
		t.Fatal("fixture TDD phases changed the test source")
	}
	commitReviewCode(t, repo)
	if err := engine.Store.Put(ctx, "tdd_checks", "a-review-red", reviewTDDCheck(task, "red", false, redSnapshot, redProduction, redTests, redFiles, redArtifact.ID), 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Put(ctx, "tdd_checks", "b-review-green", reviewTDDCheck(task, "green", true, greenSnapshot, greenProduction, greenTests, greenFiles, greenArtifact.ID), 0); err != nil {
		t.Fatal(err)
	}

	evidence, err := step.reviewExecutionEvidence()
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := agents.Render("review.md", map[string]string{
		"Plan":         jsonText(plan),
		"Requirements": jsonText(plan.Requirements),
		"Previous":     "[]",
		"Diff":         reviewFixtureDiff(t, ctx, repo),
		"Evidence":     evidence,
		"Source":       jsonText(task.Issue),
	})
	if err != nil {
		t.Fatal(err)
	}
	prompt += step.reviewCheckInstructions()
	pack, err := (ctxengine.Index{Commit: "candidate-head", Files: map[string]ctxengine.Entry{}}).Pack(ctxengine.PackRequest{
		Required:   prompt,
		Role:       "review",
		PolicyHash: project.PolicyHash,
	})
	if err != nil {
		t.Fatalf("host evidence and review prompt must fit the unchanged default context allowance: prompt_bytes=%d limit=%d: %v", len(prompt), ctxengine.DefaultInputAllowance, err)
	}
	if pack.EstimatedTokens > ctxengine.DefaultInputAllowance {
		t.Fatalf("packed review prompt exceeds default: %d > %d", pack.EstimatedTokens, ctxengine.DefaultInputAllowance)
	}
	for _, required := range []string{
		"REQ-RESULT", "qlty-check", `"command":["qlty","check","--all","--sarif"]`,
		`"exit_code":0`, `"passed":true`, `"output_artifact":"qlty-output-artifact"`,
		`"execution_id":"execution-qlty-check"`, `"cleanup_verified":true`,
		`"phase":"red"`, `"phase":"green"`, redArtifact.ID, greenArtifact.ID,
		"full output retained in host artifact",
	} {
		if !strings.Contains(pack.Prompt, required) {
			t.Errorf("projected review evidence omitted %q", required)
		}
	}
	storedAfter, err := engine.Store.Get(ctx, "verification", task.VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	if string(storedAfter.Data) != string(storedBefore.Data) {
		t.Fatal("review prompt projection mutated the stored verification report")
	}
	for _, artifact := range []struct {
		id      string
		content []byte
	}{{redArtifact.ID, redContentBefore}, {greenArtifact.ID, greenContentBefore}} {
		_, contentAfter, err := engine.Store.Artifact(ctx, artifact.id)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(contentAfter, artifact.content) {
			t.Fatalf("host TDD artifact %s changed in storage", artifact.id)
		}
	}
}

func writeReviewEvidenceFixture(t *testing.T, repo string) {
	t.Helper()
	for name, content := range map[string]string{
		"result.go":      "package result\n\nfunc Result() bool {\n\treturn false\n}\n",
		"result_test.go": "package result\n\nimport \"testing\"\n\nfunc TestResult(t *testing.T) {\n\tif !Result() {\n\t\tt.Fatal(\"Result must return true\")\n\t}\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "result.go", "result_test.go"}, {"commit", "-m", "review evidence fixture"}} {
		command := exec.Command("git", append([]string{"-C", repo}, args...)...)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
}

func commitReviewCode(t *testing.T, repo string) {
	t.Helper()
	command := exec.Command("git", "-C", repo, "add", "result.go")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add review candidate: %s: %v", output, err)
	}
	command = exec.Command("git", "-C", repo, "commit", "-m", "implement result")
	command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit review candidate: %s: %v", output, err)
	}
}

func reviewFixtureDiff(t *testing.T, ctx context.Context, repo string) string {
	t.Helper()
	return runFixtureCommand(t, ctx, repo, "git", "diff", "HEAD~2..HEAD")
}

func runFixtureCommand(t *testing.T, ctx context.Context, repo, name string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, name, append([]string{"-C", repo}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %s: %v", name, args, output, err)
	}
	return string(output)
}

func reviewEvidenceResult(name string, command []string, output, artifact string) verification.Result {
	code := 0
	result := verification.Result{
		Runtime: verification.RuntimeIdentity{ExecutionID: "execution-" + name, Generation: 1, Kind: "openshell", SandboxName: "sandbox-" + name, SandboxID: "sandbox-id-" + name, GatewayName: "whirlwind-test-local", GatewayEndpoint: "http://127.0.0.1:8080", Workspace: "default", CleanupVerified: true},
		Command: command, TimeoutSeconds: 600, StartedAt: "2026-11-07T00:00:00Z", FinishedAt: "2026-11-07T00:00:01Z",
		ExitCode: &code, Name: name, Passed: true, Output: output, OutputArtifact: artifact, DurationMS: 1000,
	}
	return result
}

func reviewSarif(size int) string {
	base := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty","rules":[]}},"results":[]}]}`
	if len(base) >= size {
		return base
	}
	return strings.TrimSuffix(base, "}") + `,"diagnostic":"` + strings.Repeat("x", size-len(base)-17) + `"}]}`
}

func saveReviewTDDArtifacts(t *testing.T, database *store.Store, ctx context.Context) (model.Artifact, model.Artifact) {
	t.Helper()
	if err := database.Put(ctx, "sessions", "review-red-attempt", map[string]string{"id": "review-red-attempt"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := database.Put(ctx, "sessions", "review-green-attempt", map[string]string{"id": "review-green-attempt"}, 0); err != nil {
		t.Fatal(err)
	}
	redOutput := strings.Repeat("=== RUN   TestResult\n--- FAIL: TestResult (0.00s)\n", 80)
	greenOutput := strings.Repeat("=== RUN   TestResult\n--- PASS: TestResult (0.00s)\n", 80)
	red, err := database.Capture(ctx, "review-red-attempt", "tdd_check:test", "text/plain", []byte(redOutput), 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	green, err := database.Capture(ctx, "review-green-attempt", "tdd_check:test", "text/plain", []byte(greenOutput), 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	return red, green
}

func reviewTDDCheck(task model.Task, phase string, passed bool, snapshot, production, tests string, files map[string]string, artifact string) tddCheck {
	exit := 1
	if passed {
		exit = 0
	}
	return tddCheck{
		RawResult: &verification.Result{Command: []string{"go", "test", "-json", "./..."}, ExitCode: &exit, Name: "test", Passed: passed, OutputArtifact: artifact},
		Files:     files, Task: task.ID, Unit: "result", PlanVersion: task.PlanVersion, CheckID: "test", Passed: passed,
		Phase: phase, Snapshot: snapshot, Production: production, Tests: tests, AttemptID: artifact,
		OutputArtifact: artifact, At: "2026-11-07T00:00:00Z",
	}
}

func TestReviewOutputExcerptRetainsBoundedHeadAndTail(t *testing.T) {
	output := strings.Repeat("head", 400) + "diagnostic-tail"
	excerpt := reviewOutputExcerpt(output)
	if len(excerpt) > 1100 || !strings.HasPrefix(excerpt, "head") || !strings.HasSuffix(excerpt, "diagnostic-tail") {
		t.Fatalf("review output excerpt must preserve bounded head and tail: bytes=%d excerpt=%q", len(excerpt), excerpt)
	}
	if !strings.Contains(excerpt, "full output retained in host artifact") {
		t.Fatalf("review output excerpt must reference complete host evidence: %q", excerpt)
	}
}
