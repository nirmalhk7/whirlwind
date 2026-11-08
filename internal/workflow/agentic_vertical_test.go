package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
	"whirlwind/internal/verification"
)

const agenticFixtureConnection = "mcp://forge"
const agenticResultFile = "result.go"
const agenticRegressionFile = "result_test.go"

// The fixture replaces only native provider execution. Git operations, checks,
// approval, evidence, submission organization and reconciliation remain real.
type agenticVerticalAdapter struct {
	mu           sync.Mutex
	prompt       string
	preserveDebt bool
}

func (a *agenticVerticalAdapter) Run(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
	if strings.HasPrefix(in.Purpose, "integration:") {
		return provider.Output{}, fmt.Errorf("remote integration operation %q is forbidden in local delivery", in.Purpose)
	}
	switch in.Role {
	case "plan":
		plan := model.Plan{Objective: "Return useful result", What: "Return true from Result with regression coverage", Why: "Consumers currently receive false", Benefit: "Consumers receive a usable result", Where: []string{agenticResultFile, agenticRegressionFile}, AcceptanceCriteria: []string{"Result returns true", "Regression test fails before the change and passes afterwards"}, Scope: []string{"Change Result and add a regression test"}, Exclusions: []string{"Do not change unrelated tests"}, Steps: []string{"Add the failing regression", "Implement Result", "Run the approved test"}, RepositoryTestCommands: [][]string{{"go", "test", "-json", "./..."}}, RepositoryTestInventory: "Configured: .whirlwind.yaml: go test -json ./...", RepositoryBuildInventory: "None configured: .whirlwind.yaml inspected", Requirements: []model.Requirement{{ID: "REQ-RESULT", Outcome: "Result returns true", EvidenceMethod: "Run TestResult"}}, Units: []model.Unit{{ID: "result", Objective: "Test first, implement Result, verify regression", AllowedPaths: []string{agenticResultFile, agenticRegressionFile}}}}
		text, _ := json.Marshal(plan)
		return provider.Output{Text: string(text)}, nil
	case "code":
		return agenticFixtureCoding(ctx, in, hooks, a.preserveDebt)
	case "review":
		if !strings.Contains(in.Prompt, "Host execution evidence") || !strings.Contains(in.Prompt, `"phase":"red"`) || !strings.Contains(in.Prompt, `"phase":"green"`) {
			return provider.Output{}, fmt.Errorf("review omitted actual host TDD evidence")
		}
		text, _ := json.Marshal(model.Review{Requirements: []model.RequirementResult{{ID: "REQ-RESULT", Status: "pass", Code: []string{agenticResultFile, agenticRegressionFile}, Evidence: []string{"TestResult passed after a recorded failing run"}, Explanation: "Result now returns true and the regression test passes."}}, Findings: []model.Finding{}, Summary: "The candidate satisfies the approved requirement."})
		return provider.Output{Text: string(text)}, nil
	case "finalizer":
		a.mu.Lock()
		a.prompt = in.Prompt
		a.mu.Unlock()
		return provider.Output{Text: "Exact reviewed candidate is ready for local delivery."}, nil
	default:
		return provider.Output{}, fmt.Errorf("unexpected Agentic role: %s", in.Role)
	}
}

func agenticFixtureCoding(ctx context.Context, in provider.Input, hooks provider.Hooks, preserveDebt bool) (provider.Output, error) {
	if hooks.RunCheck == nil {
		return provider.Output{}, fmt.Errorf("coding host check broker unavailable")
	}
	checkID := "test"
	if preserveDebt {
		checkID = "focused"
	}
	if err := os.WriteFile(filepath.Join(in.CWD, agenticResultFile), []byte("package result\n\nfunc Result() bool {\n\treturn false\n}\n"), 0600); err != nil {
		return provider.Output{}, err
	}
	test := "package result\n\nimport \"testing\"\n\nfunc TestResult(t *testing.T) {\n\tif !Result() {\n\t\tt.Fatal(\"Result must return true\")\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(in.CWD, agenticRegressionFile), []byte(test), 0600); err != nil {
		return provider.Output{}, err
	}
	red, err := hooks.RunCheck(ctx, checkID)
	if err != nil || red.Passed || !strings.Contains(red.Output, "TestResult") {
		return provider.Output{}, fmt.Errorf("expected failing regression: %+v %v", red, err)
	}
	if err := os.WriteFile(filepath.Join(in.CWD, agenticResultFile), []byte("package result\n\nfunc Result() bool {\n\treturn true\n}\n"), 0600); err != nil {
		return provider.Output{}, err
	}
	green, err := hooks.RunCheck(ctx, checkID)
	if err != nil || !green.Passed || green.Phase != "green" {
		return provider.Output{}, fmt.Errorf("expected passing regression: %+v %v", green, err)
	}
	return provider.Output{Text: "Regression red and green recorded; Result implemented"}, nil
}

func TestAgenticWorkflowCompletesWithoutGitHubCLI(t *testing.T) {
	testAgenticWorkflow(t, false)
}

func TestAgenticWorkflowPreservesExistingTestDebt(t *testing.T) {
	testAgenticWorkflow(t, true)
}

func TestAgenticGoModuleCacheCleanupRestoresOnlyOwnedPermissions(t *testing.T) {
	root := t.TempDir()
	moduleCache := filepath.Join(root, "go-home", "go", "pkg", "mod")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(moduleCache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	moduleFile := filepath.Join(moduleCache, "toolchain")
	outsideFile := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(moduleFile, []byte("owned cache"), 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideFile, []byte("outside cache"), 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(moduleCache, 0500); err != nil {
		t.Fatal(err)
	}
	if err := restoreAgenticGoModuleCachePermissions(moduleCache); err != nil {
		t.Fatal(err)
	}
	assertOwnerWritable(t, moduleFile)
	assertNotOwnerWritable(t, outsideFile)
}

func assertOwnerWritable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0200 == 0 {
		t.Fatalf("owned Go module cache file is not removable: %q mode=%v", path, info.Mode().Perm())
	}
}

func assertNotOwnerWritable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0200 != 0 {
		t.Fatalf("path outside owned Go module cache was changed: %q mode=%v", path, info.Mode().Perm())
	}
}

func restoreAgenticGoModuleCachePermissions(root string) error {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	return filepath.Walk(root, restoreAgenticCachePathPermissions)
}

func restoreAgenticCachePathPermissions(path string, info os.FileInfo, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	permissions := info.Mode().Perm()
	switch {
	case info.IsDir():
		permissions |= 0300
	case info.Mode().IsRegular():
		permissions |= 0200
	default:
		return nil
	}
	if permissions == info.Mode().Perm() {
		return nil
	}
	if err := os.Chmod(path, permissions); err != nil {
		return fmt.Errorf("restore owned cache permissions for %q: %w", path, err)
	}
	return nil
}

func testAgenticWorkflow(t *testing.T, preexistingDebt bool) {
	fixture := openshell.Install(t)
	home, cache := t.TempDir(), t.TempDir()
	fixture.EnableGoTestPayloadExecution(home, cache)
	moduleCache := filepath.Join(home, "go", "pkg", "mod")
	t.Cleanup(func() {
		if err := restoreAgenticGoModuleCachePermissions(moduleCache); err != nil {
			t.Errorf("restore permissions for fixture-owned Go module cache: %v", err)
		}
	})
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	e, task, repo := testEngine(t)
	f := agenticWorkflowFixture{t: t, ctx: context.Background(), e: e, task: task, repo: repo, debt: preexistingDebt}
	f.prepareRepository()
	f.configureNativeProvider()
	f.approveLocalPlan()
	if preexistingDebt {
		f.verifyExistingTestDebtBlocksAcceptance()
		return
	}
	done := f.completeLocalDelivery()
	f.verifyTDD(done)
	f.verifySubmission(done)
	f.reconcile(done)
}

func (f *agenticWorkflowFixture) verifyExistingTestDebtBlocksAcceptance() {
	t, ctx, engine := f.t, f.ctx, f.e
	if err := engine.Step(ctx, f.task.ID); err != nil {
		t.Fatalf("run candidate verification with preserved test debt: %v", err)
	}
	blocked, _ := localStoredTask(t, engine, f.task.ID)
	if blocked.Stage != "repair" || blocked.ChecksPassed || blocked.ReviewID != "" || blocked.Delivery != nil || blocked.VerificationID == "" {
		t.Fatalf("failed configured test did not block review and local finalization: %+v", blocked)
	}
	record, err := engine.Store.Get(ctx, "verification", blocked.VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		Stage  string              `json:"stage"`
		Report verification.Report `json:"report"`
	}
	if err := json.Unmarshal(record.Data, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Stage != "failed" || evidence.Report.Passed {
		t.Fatalf("configured test failure was not retained in verification evidence: %+v", evidence)
	}
	var debt *verification.Result
	for index := range evidence.Report.Results {
		result := &evidence.Report.Results[index]
		if result.Name == "test" {
			debt = result
			break
		}
	}
	if debt == nil || debt.Passed || debt.ExitCode == nil || *debt.ExitCode == 0 || debt.OutputArtifact == "" {
		t.Fatalf("failed configured test lacks process evidence: %+v", debt)
	}
	_, rawOutput, err := engine.Store.Artifact(ctx, debt.OutputArtifact)
	if err != nil || !strings.Contains(string(rawOutput), "TestLegacyDebt") {
		t.Fatalf("full failed-test evidence was not retained: err=%v output=%q", err, rawOutput)
	}
	reviews, err := engine.Store.List(ctx, "reviews")
	if err != nil {
		t.Fatal(err)
	}
	for _, saved := range reviews {
		review, err := store.Decode[model.Review](saved)
		if err != nil {
			t.Fatal(err)
		}
		if review.Provenance != nil && review.Provenance.Task == f.task.ID {
			t.Fatalf("reviewer ran despite failed configured test: %+v", review)
		}
	}
	f.adapter.mu.Lock()
	finalizerPrompt := f.adapter.prompt
	f.adapter.mu.Unlock()
	if finalizerPrompt != "" {
		t.Fatalf("finalizer ran despite failed configured test: %q", finalizerPrompt)
	}
	legacySource := submissionGit(t, f.repo, "show", "HEAD:legacy_test.go")
	if got := submissionGit(t, blocked.Worktree, "show", "HEAD:legacy_test.go"); got != legacySource {
		t.Fatal("candidate removed or changed the pre-existing failing test")
	}
	if got := submissionGit(t, blocked.Worktree, "show", "HEAD:result.go"); !strings.Contains(got, "return true") {
		t.Fatalf("candidate implementation state was not preserved: %s", got)
	}
	if got := submissionGit(t, f.repo, "show", "HEAD:result.go"); !strings.Contains(got, "return false") {
		t.Fatalf("coding changed original checkout: %s", got)
	}
	f.verifyTDD(blocked)
}

type agenticWorkflowFixture struct {
	t            *testing.T
	ctx          context.Context
	e            *Engine
	task         model.Task
	project      model.Project
	repo, marker string
	debt         bool
	adapter      *agenticVerticalAdapter
}

func (f *agenticWorkflowFixture) prepareRepository() {
	t, e, repo := f.t, f.e, f.repo
	e.PlansRoot = filepath.Join(t.TempDir(), "plans")
	bin := t.TempDir()
	marker := filepath.Join(bin, "gh-called")
	f.marker = marker
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho invoked >> '"+marker+"'\nexit 97\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	e.GitHub = github.CLI{}
	tddWrite(t, repo, "go.mod", "module example.test/agentic\ngo 1.26.10\n")
	tddWrite(t, repo, agenticResultFile, "package result\n\nfunc Result() bool {\n\treturn false\n}\n")
	if f.debt {
		tddWrite(t, repo, "legacy_test.go", "package result\nimport \"testing\"\nfunc TestLegacyDebt(t *testing.T) { t.Fatal(\"existing debt outside selected objective\") }\n")
	}
	checks := "  - name: test\n    command: [go, test, -json, ./...]\n"
	if f.debt {
		checks += "  - name: focused\n    command: [go, test, -json, -run, '^TestResult$', ./...]\n"
	}
	policyYAML := "budget:\n  max_attempts: 100\n  max_runtime_seconds: 3600\n  verification_attempts: 10\n  verification_runtime_seconds: 120\nchecks:\n" + checks + "allowed_paths: ['**']\nrisk_rules:\n  - level: high\n    paths: ['auth/**']\ncommands: []\n"
	tddWrite(t, repo, ".whirlwind.yaml", policyYAML)
	seedAgenticQltyConfig(t, repo)
	submissionGit(t, repo, "add", "--all")
	submissionGit(t, repo, "commit", "-m", "prepare result module")
}

func seedAgenticQltyConfig(t *testing.T, repo string) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository Qlty configuration")
	}
	sourceDirectory := filepath.Join(filepath.Dir(source), "..", "..", ".qlty")
	destinationDirectory := filepath.Join(repo, ".qlty")
	if err := os.MkdirAll(destinationDirectory, 0700); err != nil {
		t.Fatalf("create fixture Qlty configuration directory: %v", err)
	}
	for _, name := range []string{"qlty.toml", ".gitignore"} {
		config, err := os.ReadFile(filepath.Join(sourceDirectory, name))
		if err != nil {
			t.Fatalf("read repository Qlty file %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(destinationDirectory, name), config, 0600); err != nil {
			t.Fatalf("seed fixture Qlty file %s: %v", name, err)
		}
	}
}

func (f *agenticWorkflowFixture) configureNativeProvider() {
	t, ctx, e, task, repo := f.t, f.ctx, f.e, f.task, f.repo
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Policy, project.PolicyHash, err = policy.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	projectRecord, _ := e.Store.Get(ctx, "projects", project.ID)
	if err := e.Store.Put(ctx, "projects", project.ID, project, projectRecord.Version); err != nil {
		t.Fatal(err)
	}
	config := model.WorkflowConfig{ActiveMode: model.AgenticMode, ConfiguredMode: model.AgenticMode}
	if err := e.Store.Put(ctx, "workflow_config", "global", config, 0); err != nil {
		t.Fatal(err)
	}
	task.Mode, task.Stage = model.AgenticMode, "planning"
	task.Issue = model.Issue{Title: "Return useful result", Body: "Result must return true"}
	task.Selection.PolicyHash = project.PolicyHash
	task.Sources = []model.SourceRef{{Provider: "fake", Host: "issues.example", Connection: "mcp://issues", Repository: "APP", ID: "APP-3", URL: "https://issues.example/APP-3"}}
	taskRecord, _ := e.Store.Get(ctx, "tasks", task.ID)
	if err := e.Store.Put(ctx, "tasks", task.ID, task, taskRecord.Version); err != nil {
		t.Fatal(err)
	}
	f.task, f.project = task, project
	f.enableLocalAdapter()
}

func (f *agenticWorkflowFixture) enableLocalAdapter() {
	e := f.e
	adapter := &agenticVerticalAdapter{preserveDebt: f.debt}
	f.adapter = adapter
	e.Adapter = func(model.Provider) provider.Adapter { return adapter }
}

func (f *agenticWorkflowFixture) approveLocalPlan() {
	t, ctx, e, task := f.t, f.ctx, f.e, f.task
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	pending, version := localStoredTask(t, e, task.ID)
	if pending.Stage != "plan_approval" || pending.Issue.Number != 0 || pending.Worktree != "" {
		t.Fatalf("Agentic planning skipped local approval: %+v", pending)
	}
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := localStoredTask(t, e, task.ID)
	if unchanged.Stage != "plan_approval" || unchanged.Worktree != "" {
		t.Fatal("unapproved local plan started coding")
	}
	view, err := e.ReadLocalPlan(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReviewLocalPlan(ctx, task.ID, version, "approve", view.Hash, ""); err != nil {
		t.Fatal(err)
	}
}

func (f *agenticWorkflowFixture) completeLocalDelivery() model.Task {
	t, ctx, e, task := f.t, f.ctx, f.e, f.task
	for i := 0; i < 20; i++ {
		current, version := localStoredTask(t, e, task.ID)
		if current.Stage == "complete" {
			break
		}
		if current.Stage == "approval" {
			approval := LocalFinalizationApproval{PlanVersion: current.PlanVersion, ApprovedPlanHash: current.PlanDocument.ApprovedHash, ReviewedCommit: current.ReviewedCommit, ReviewID: current.ReviewID, VerificationID: current.VerificationID, PolicyHash: f.project.PolicyHash}
			if _, err := e.ApproveLocalFinalization(ctx, task.ID, version, approval, "agentic-local-finalization-approval"); err != nil {
				t.Fatalf("approve exact local finalization: %v", err)
			}
			continue
		}
		if err := e.Step(ctx, task.ID); err != nil {
			latest, _ := localStoredTask(t, e, task.ID)
			verification := storedWorkflowEvidence(ctx, e.Store, "verification", latest.VerificationID)
			review := storedWorkflowEvidence(ctx, e.Store, "reviews", latest.ReviewID)
			logs := agenticSnapshotPathDiagnostic(ctx, latest.Worktree, ".qlty/logs")
			t.Fatalf("stage %s: %v; task error=%q handoff=%+v verification=%s review=%s snapshot path=%s", current.Stage, err, latest.Error, latest.Handoff, verification, review, logs)
		}
	}
	done, _ := localStoredTask(t, e, task.ID)
	if done.Stage != "complete" || done.Delivery == nil || done.Delivery.Stage != "complete" || !done.ChecksPassed {
		t.Fatalf("Agentic workflow did not finish local delivery: stage=%s delivery=%+v checks=%v", done.Stage, done.Delivery, done.ChecksPassed)
	}
	if done.VerificationID == "" || done.ReviewID == "" || done.Delivery.ReviewedCommit != done.ReviewedCommit {
		t.Fatalf("completed delivery lost verification/review/candidate evidence: %+v", done)
	}
	return done
}

func agenticSnapshotPathDiagnostic(ctx context.Context, root, relative string) string {
	path := filepath.Join(root, relative)
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Sprintf("path=%q lstat_error=%v inventory=%s", path, err, agenticQltyInventory(ctx, root, relative))
	}
	target, linkErr := os.Readlink(path)
	tracked, trackedErr := github.Run(ctx, root, "git", "ls-files", "--stage", "--", relative)
	ignored, ignoredErr := github.Run(ctx, root, "git", "check-ignore", "-v", "--", relative)
	return fmt.Sprintf("path=%q mode=%s symlink=%v readlink=%q readlink_error=%v tracked=%q tracked_error=%v ignored=%q ignored_error=%v inventory=%s qlty_logs=%s", path, info.Mode(), info.Mode()&os.ModeSymlink != 0, target, linkErr, tracked, trackedErr, ignored, ignoredErr, agenticQltyInventory(ctx, root, relative), agenticQltyLogs(path))
}

func agenticQltyInventory(ctx context.Context, root, relative string) string {
	inventory, inventoryErr := github.Run(ctx, root, "git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	status, statusErr := github.Run(ctx, root, "git", "status", "--ignored", "--short", "--", relative)
	return fmt.Sprintf("ls-files=%q ls-files_error=%v status=%q status_error=%v", agenticBoundedText(inventory, 8192), inventoryErr, status, statusErr)
}

func agenticQltyLogs(directory string) string {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Sprintf("directory=%q read_error=%v", directory, err)
	}
	var newest os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "qlty-cli.") {
			continue
		}
		newest = entry
	}
	if newest == nil {
		return fmt.Sprintf("directory=%q entries=%v", directory, agenticQltyLogNames(entries))
	}
	path := filepath.Join(directory, newest.Name())
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		return fmt.Sprintf("file=%q read_error=%v", path, readErr)
	}
	return fmt.Sprintf("file=%q contents=%q", path, agenticBoundedText(string(raw), 65536))
}

func agenticQltyLogNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func agenticBoundedText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}

func storedWorkflowEvidence(ctx context.Context, database *store.Store, kind, id string) string {
	if id == "" {
		return "<missing id>"
	}
	record, err := database.Get(ctx, kind, id)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(record.Data)
}

func (f *agenticWorkflowFixture) verifyTDD(done model.Task) {
	t, ctx, e, task := f.t, f.ctx, f.e, f.task
	redGreen, err := e.Store.List(ctx, "tdd_checks")
	if err != nil {
		t.Fatal(err)
	}
	phases := map[string]bool{}
	var red, green *tddCheck
	for _, record := range redGreen {
		entry, err := store.Decode[tddCheck](record)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Task == task.ID {
			phases[entry.Phase] = true
			if entry.Phase == "red" {
				red = &entry
			}
			if entry.Phase == "green" {
				green = &entry
			}
		}
	}
	if !phases["red"] || !phases["green"] {
		t.Fatalf("real regression evidence missing: %+v", phases)
	}
	if f.debt {
		f.verifyDebtTDDPair(red, green)
	}
	if f.debt && submissionGit(t, done.Worktree, "show", "HEAD:legacy_test.go") != submissionGit(t, f.repo, "show", "HEAD:legacy_test.go") {
		t.Fatal("submission changed unrelated failing test")
	}
}

func (f *agenticWorkflowFixture) verifyDebtTDDPair(red, green *tddCheck) {
	t := f.t
	if red == nil || red.Passed || red.RawResult == nil || red.RawResult.ExitCode == nil || *red.RawResult.ExitCode == 0 {
		t.Fatalf("TDD red phase lacks its actual failing test result: %+v", red)
	}
	if green == nil || !green.Passed || green.RawResult == nil || green.RawResult.ExitCode == nil || *green.RawResult.ExitCode != 0 {
		t.Fatalf("TDD green phase lacks its actual passing focused test result: %+v", green)
	}
	for _, entry := range []*tddCheck{red, green} {
		_, output, err := f.e.Store.Artifact(f.ctx, entry.OutputArtifact)
		if err != nil || !strings.Contains(string(output), "TestResult") {
			t.Fatalf("TDD %s output artifact lacks TestResult evidence: err=%v output=%q", entry.Phase, err, output)
		}
	}
}

func (f *agenticWorkflowFixture) verifySubmission(done model.Task) {
	t, task, adapter := f.t, f.task, f.adapter
	adapter.mu.Lock()
	prompt := adapter.prompt
	adapter.mu.Unlock()
	if !strings.Contains(prompt, "REQ-RESULT") || !strings.Contains(prompt, task.ID) || strings.Contains(prompt, "create PR") {
		t.Fatalf("local finalizer prompt omitted reviewed provenance/requirements or requested remote publication: %s", prompt)
	}
	if done.Delivery == nil || done.Delivery.Stage != "complete" {
		t.Fatalf("local delivery record missing: %+v", done.Delivery)
	}
	if got := submissionGit(t, done.Delivery.Worktree, submissionRevParse, "HEAD^{tree}"); got != submissionGit(t, done.Worktree, submissionRevParse, done.ReviewedCommit+"^{tree}") {
		t.Fatal("local delivery changed accepted tree")
	}
	if got := submissionGit(t, done.Delivery.Worktree, "show", "-s", "--format=%s", "refs/heads/"+done.Delivery.Branch); got == "" {
		t.Fatal("local delivery branch does not reference the reviewed commit")
	}
	if got := submissionGit(t, done.Delivery.Worktree, submissionRevParse, "refs/heads/"+done.Delivery.Branch); got != done.ReviewedCommit {
		t.Fatalf("local delivery branch differs from reviewed HEAD: %s", got)
	}
	publications, err := f.e.Store.List(f.ctx, "publications")
	if err != nil || len(publications) != 0 || done.PR != "" {
		t.Fatalf("local delivery attempted remote publication: PR=%q records=%d err=%v", done.PR, len(publications), err)
	}
}

func (f *agenticWorkflowFixture) reconcile(done model.Task) {
	t, e, task := f.t, f.e, f.task
	completed, _ := localStoredTask(t, e, task.ID)
	if completed.Stage != "complete" {
		t.Fatalf("local delivery did not complete task: %+v", completed)
	}
	if contents, err := os.ReadFile(f.marker); !os.IsNotExist(err) {
		t.Fatalf("Agentic workflow invoked gh: %s %v", contents, err)
	}
	if got := submissionGit(t, f.repo, "show", "HEAD:result.go"); !strings.Contains(got, "return false") {
		t.Fatal("coding modified original checkout")
	}
}
