package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const agenticFixtureRemote = "https://forge.example/team/product"
const agenticFixturePR = "https://forge.example/team/product/pull/17"
const agenticFixtureConnection = "mcp://forge"
const agenticResultFile = "result.go"
const agenticRegressionFile = "result_test.go"
const agenticResultCommit = "fix: return useful result with regression coverage"

// The fixture replaces only native provider execution. Git operations, checks,
// approval, evidence, submission organization and reconciliation remain real.
type agenticVerticalAdapter struct {
	mu       sync.Mutex
	created  bool
	merged   bool
	body     string
	requests []string
}

func (a *agenticVerticalAdapter) Run(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
	catalog := []model.Integration{{Provider: "fake", Ref: agenticFixtureConnection, Name: "Forge", Kind: "mcp", Enabled: true, Callable: true, Tools: []model.IntegrationTool{{Name: "repository_metadata", ReadOnly: true}, {Name: "lookup_pr", ReadOnly: true}, {Name: "create_pr"}, {Name: "pr_state", ReadOnly: true}}}}
	if in.CatalogOnly {
		return provider.Output{Integrations: catalog}, nil
	}
	if !in.NativeIntegrations {
		return provider.Output{}, fmt.Errorf("Agentic request omitted native integrations: %s/%s", in.Role, in.Purpose)
	}
	if strings.HasPrefix(in.Purpose, "integration:") {
		if len(in.RequiredIntegrations) != 1 || in.RequiredIntegrations[0].Ref != agenticFixtureConnection || in.RequiredIntegrations[0].Provider != "fake" {
			return provider.Output{}, fmt.Errorf("native operation used wrong publication connection: %+v", in.RequiredIntegrations)
		}
		return a.repositoryOperation(in, catalog)
	}
	switch in.Role {
	case "plan":
		plan := model.Plan{Objective: "Return useful result", What: "Return true from Result with regression coverage", Why: "Consumers currently receive false", Benefit: "Consumers receive a usable result", Where: []string{agenticResultFile, agenticRegressionFile}, AcceptanceCriteria: []string{"Result returns true", "Regression test fails before the change and passes afterwards"}, Units: []model.Unit{{ID: "result", Objective: "Test first, implement Result, verify regression", AllowedPaths: []string{agenticResultFile, agenticRegressionFile}}}}
		text, _ := json.Marshal(plan)
		return provider.Output{Text: string(text)}, nil
	case "code":
		return agenticFixtureCoding(ctx, in, hooks)
	case "review":
		if !strings.Contains(in.Prompt, "Host execution evidence") || !strings.Contains(in.Prompt, `"phase":"red"`) || !strings.Contains(in.Prompt, `"phase":"green"`) {
			return provider.Output{}, fmt.Errorf("review omitted actual host TDD evidence")
		}
		return fakeAgent{9}.Run(ctx, in, hooks)
	case "submit":
		text, _ := json.Marshal(map[string]any{"title": "Make Result usable", "body": "Result now reports success to callers. Regression test verifies the previous failure and passing behavior.", "commits": []CommitGroup{{Message: agenticResultCommit, Files: []string{agenticResultFile, agenticRegressionFile}}}})
		return provider.Output{Text: string(text)}, nil
	default:
		return provider.Output{}, fmt.Errorf("unexpected Agentic role: %s", in.Role)
	}
}

func agenticFixtureCoding(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
	if hooks.RunCheck == nil {
		return provider.Output{}, fmt.Errorf("coding host check broker unavailable")
	}
	test := "package result\nimport \"testing\"\nfunc TestResult(t *testing.T) { if !Result() { t.Fatal(\"Result must return true\") } }\n"
	if err := os.WriteFile(filepath.Join(in.CWD, agenticRegressionFile), []byte(test), 0600); err != nil {
		return provider.Output{}, err
	}
	red, err := hooks.RunCheck(ctx, "test")
	if err != nil || red.Passed || !strings.Contains(red.Output, "TestResult") {
		return provider.Output{}, fmt.Errorf("expected failing regression: %+v %v", red, err)
	}
	if err := os.WriteFile(filepath.Join(in.CWD, agenticResultFile), []byte("package result\nfunc Result() bool { return true }\n"), 0600); err != nil {
		return provider.Output{}, err
	}
	green, err := hooks.RunCheck(ctx, "test")
	if err != nil || !green.Passed || green.Phase != "green" {
		return provider.Output{}, fmt.Errorf("expected passing regression: %+v %v", green, err)
	}
	return provider.Output{Text: "Regression red and green recorded; Result implemented"}, nil
}

func (a *agenticVerticalAdapter) repositoryOperation(in provider.Input, catalog []model.Integration) (provider.Output, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	operation := strings.TrimPrefix(in.Purpose, "integration:")
	a.requests = append(a.requests, operation)
	result := map[string]any{}
	switch operation {
	case "repository_metadata":
		result = map[string]any{"default_branch": "main", "repository": agenticFixtureRemote, "lookup_pr_tool": "lookup_pr", "create_pr_tool": "create_pr"}
	case "lookup_pr", "create_pr":
		args, err := agenticPublicationArguments(in)
		if err != nil {
			return provider.Output{}, err
		}
		if operation == "create_pr" {
			if in.ReadOnly {
				return provider.Output{}, fmt.Errorf("creation requested read-only execution")
			}
			a.created, a.body = true, args["body"]
		}
		result = map[string]any{"found": a.created, "repository": agenticFixtureRemote, "head_sha": args["head_sha"]}
		if a.created {
			result["url"], result["head"], result["base"] = agenticFixturePR, args["head"], args["base"]
		}
	case "pr_state":
		state := "OPEN"
		if a.merged {
			state = "MERGED"
		}
		result["state"] = state
	default:
		return provider.Output{}, fmt.Errorf("unexpected integration operation %s", operation)
	}
	text, _ := json.Marshal(result)
	return provider.Output{Text: string(text), Integrations: catalog, ToolCalls: []provider.ToolEvidence{{Ref: agenticFixtureConnection, Tool: operation, CallID: "native-" + operation, Success: true}}}, nil
}

func agenticPublicationArguments(in provider.Input) (map[string]string, error) {
	start := strings.Index(in.Prompt, "Arguments: ")
	if start < 0 {
		return nil, fmt.Errorf("publication missing explicit arguments")
	}
	var args map[string]string
	if err := json.NewDecoder(strings.NewReader(in.Prompt[start+len("Arguments: "):])).Decode(&args); err != nil {
		return nil, err
	}
	if args["repository"] != agenticFixtureRemote || args["base"] != "main" || !strings.HasPrefix(args["head"], "codex/") {
		return nil, fmt.Errorf("wrong repository publication request: %+v", args)
	}
	return args, nil
}

func TestAgenticWorkflowCompletesWithoutGitHubCLI(t *testing.T) {
	testAgenticWorkflow(t, false)
}

func TestAgenticWorkflowPreservesExistingTestDebt(t *testing.T) {
	testAgenticWorkflow(t, true)
}

func testAgenticWorkflow(t *testing.T, preexistingDebt bool) {
	e, task, repo := testEngine(t)
	f := agenticWorkflowFixture{t: t, ctx: context.Background(), e: e, task: task, repo: repo, debt: preexistingDebt}
	f.prepareRepository()
	f.configureNativeProvider()
	f.approveLocalPlan()
	done := f.publish()
	f.verifyTDD(done)
	f.verifySubmission(done)
	f.reconcile(done)
}

type agenticWorkflowFixture struct {
	t            *testing.T
	ctx          context.Context
	e            *Engine
	task         model.Task
	project      model.Project
	repo, marker string
	bare         string
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
	tddWrite(t, repo, "go.mod", "module example.test/agentic\ngo 1.24\n")
	tddWrite(t, repo, agenticResultFile, "package result\nfunc Result() bool { return false }\n")
	if f.debt {
		tddWrite(t, repo, "legacy_test.go", "package result\nimport \"testing\"\nfunc TestLegacyDebt(t *testing.T) { t.Fatal(\"existing debt outside selected objective\") }\n")
	}
	policyYAML := "budget:\n  max_attempts: 100\n  max_runtime_seconds: 3600\n  verification_attempts: 10\n  verification_runtime_seconds: 120\nchecks:\n  - name: test\n    command: [go, test, -json, ./...]\nallowed_paths: ['**']\nrisk_rules:\n  - level: high\n    paths: ['auth/**']\ncommands: []\n"
	tddWrite(t, repo, ".whirlwind.yaml", policyYAML)
	submissionGit(t, repo, "add", "--all")
	submissionGit(t, repo, "commit", "-m", "prepare result module")
	f.bare = nativeFixtureOrigin(t, repo, agenticFixtureRemote)
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
	project.RemoteURL, project.DefaultBranch = agenticFixtureRemote, "main"
	projectRecord, _ := e.Store.Get(ctx, "projects", project.ID)
	if err := e.Store.Put(ctx, "projects", project.ID, project, projectRecord.Version); err != nil {
		t.Fatal(err)
	}
	config := model.WorkflowConfig{ActiveMode: model.AgenticMode, ConfiguredMode: model.AgenticMode, Integrations: model.IntegrationSettings{Discovery: "connected", Publication: map[string]model.IntegrationTarget{agenticFixtureRemote: {Provider: "fake", Ref: agenticFixtureConnection}}}}
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
	f.enableNativeAdapter()
}

func (f *agenticWorkflowFixture) enableNativeAdapter() {
	t, ctx, e, project := f.t, f.ctx, f.e, f.project
	providerRecord, err := e.Store.Get(ctx, "providers", "fake")
	if err != nil {
		t.Fatal(err)
	}
	nativeProvider, err := store.Decode[model.Provider](providerRecord)
	if err != nil {
		t.Fatal(err)
	}
	nativeProvider.Kind = "codex"
	if err := e.Store.Put(ctx, "providers", "fake", nativeProvider, providerRecord.Version); err != nil {
		t.Fatal(err)
	}
	adapter := &agenticVerticalAdapter{}
	f.adapter = adapter
	e.Adapter = func(model.Provider) provider.Adapter { return adapter }
	branch, err := e.InspectIntegrationRepository(ctx, project)
	if err != nil || branch != "main" {
		t.Fatalf("native repository setup failed: %s %v", branch, err)
	}
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

func (f *agenticWorkflowFixture) publish() model.Task {
	t, ctx, e, task, project := f.t, f.ctx, f.e, f.task, f.project
	for i := 0; i < 9; i++ {
		current, _ := localStoredTask(t, e, task.ID)
		if current.Stage == "pr_created" {
			break
		}
		if err := e.Step(ctx, task.ID); err != nil {
			t.Fatalf("stage %s: %v", current.Stage, err)
		}
	}
	done, _ := localStoredTask(t, e, task.ID)
	if done.Stage != "pr_created" || done.PR != agenticFixturePR || done.Submission == nil || done.Submission.Stage != "verified" || !done.ChecksPassed {
		t.Fatalf("Agentic workflow did not publish: %+v", done)
	}
	proof, err := PublicationEvidence(ctx, e.Store, done, project.PolicyHash)
	if err != nil || proof.Head != done.ReviewedCommit {
		t.Fatalf("publication lacks final HEAD evidence: %+v %v", proof, err)
	}
	return done
}

func (f *agenticWorkflowFixture) verifyTDD(done model.Task) {
	t, ctx, e, task := f.t, f.ctx, f.e, f.task
	redGreen, err := e.Store.List(ctx, "tdd_checks")
	if err != nil {
		t.Fatal(err)
	}
	phases := map[string]bool{}
	retainedDebt := false
	for _, record := range redGreen {
		entry, err := store.Decode[tddCheck](record)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Task == task.ID {
			phases[entry.Phase] = true
			retainedDebt = retainedDebt || agenticRetainedDebt(entry)
		}
	}
	if !phases["red"] || !phases["green"] {
		t.Fatalf("real regression evidence missing: %+v", phases)
	}
	if f.debt && !retainedDebt {
		t.Fatal("passing regression concealed raw evidence of existing test debt")
	}
	if f.debt && submissionGit(t, done.Worktree, "show", "HEAD:legacy_test.go") != submissionGit(t, f.repo, "show", "HEAD:legacy_test.go") {
		t.Fatal("submission changed unrelated failing test")
	}
}

func agenticRetainedDebt(entry tddCheck) bool {
	if entry.Phase != "green" || !entry.BaselineAccepted || entry.RawResult == nil {
		return false
	}
	return entry.RawResult.ExitCode != nil && *entry.RawResult.ExitCode == 1
}

func (f *agenticWorkflowFixture) verifySubmission(done model.Task) {
	t, task, adapter := f.t, f.task, f.adapter
	if strings.Contains(adapter.body, "Closes #") || !strings.Contains(adapter.body, "https://issues.example/APP-3") || !strings.Contains(adapter.body, "Plan: "+task.ID+" v1") {
		t.Fatalf("publication lost external source/local plan identity: %s", adapter.body)
	}
	if got := submissionGit(t, done.Worktree, "log", "--format=%s", done.Base+"..HEAD"); got != agenticResultCommit {
		t.Fatalf("unexpected public commit history: %s", got)
	}
	if got := submissionGit(t, done.Worktree, submissionRevParse, "HEAD^{tree}"); got != submissionGit(t, done.Submission.OriginalWorktree, submissionRevParse, done.Submission.ReviewedCommit+"^{tree}") {
		t.Fatal("submission changed accepted tree")
	}
	if got := submissionGit(t, f.bare, submissionRevParse, "refs/heads/"+done.Branch); got != done.ReviewedCommit {
		t.Fatal("published branch differs from reviewed HEAD")
	}
}

func (f *agenticWorkflowFixture) reconcile(done model.Task) {
	t, ctx, e, task, adapter := f.t, f.ctx, f.e, f.task, f.adapter
	adapter.mu.Lock()
	adapter.merged = true
	adapter.mu.Unlock()
	e.Reconcile(ctx)
	completed, _ := localStoredTask(t, e, task.ID)
	if completed.Stage != "complete" {
		t.Fatalf("native outcome monitoring did not complete task: %+v", completed)
	}
	outcomes, err := e.Store.List(ctx, "outcomes")
	if err != nil || len(outcomes) != 1 || !strings.Contains(string(outcomes[0].Data), `"human_decision":"merged"`) {
		t.Fatalf("merged outcome not persisted: %+v %v", outcomes, err)
	}
	if contents, err := os.ReadFile(f.marker); !os.IsNotExist(err) {
		t.Fatalf("Agentic workflow invoked gh: %s %v", contents, err)
	}
	if got := submissionGit(t, f.repo, "show", "HEAD:result.go"); !strings.Contains(got, "return false") {
		t.Fatal("coding modified original checkout")
	}
}
