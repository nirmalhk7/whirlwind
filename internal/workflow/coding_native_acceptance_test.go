package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

const (
	nativeCodingFixtureHeadTree = "HEAD^{tree}"
	nativeCodingFixtureModel    = "fixture-model"
	nativeCodingFixturePrimary  = "coding-primary"
	nativeCodingFixtureRevParse = "rev-parse"
)

const (
	nativeCodingSource = "src/result.sh"
	nativeCodingTest   = "src/test_result.sh"
	nativeCodingDelete = "src/obsolete.txt"
	nativeCodingCheck  = "regression"
)

func TestCodingNativeACPDeliversExactVerifiedTree(t *testing.T) {
	e, plan, project := nativeCodingPlan(t, nil, nil, false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e.dispatch(ctx)
	e.background.Wait()
	candidate := assertNativeCodingCandidate(t, e, project, plan)
	for n := 0; n < 5; n++ {
		task, _ := localStoredTask(t, e, plan.ID)
		if task.Stage == "complete" {
			if task.Delivery == nil || task.ReviewedCommit != candidate.CandidateCommit || task.Delivery.Tree != candidate.CandidateTree ||
				submissionGit(t, task.Delivery.Worktree, nativeCodingFixtureRevParse, nativeCodingFixtureHeadTree) != candidate.CandidateTree {
				t.Fatalf("review or delivery changed the verified tree: %+v", task.Delivery)
			}
			return
		}
		requireWorkflowSuccess(t, e.Step(ctx, plan.ID))
	}
	task, _ := localStoredTask(t, e, plan.ID)
	t.Fatalf("candidate did not reach local delivery: stage=%s error=%q", task.Stage, task.Error)
}

func TestCodingNativeACPFallbackRetainsScopeAndStopsPriorWriter(t *testing.T) {
	e, plan, project := nativeCodingPlan(t, nil, nil, true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e.dispatch(ctx)
	e.background.Wait()
	assertNativeCodingCandidate(t, e, project, plan)
	attempts := nativeCodingAttempts(t, e, plan)
	if len(attempts) != 5 {
		t.Fatalf("expected four initial provider attempts and one fallback, got %d: %v", len(attempts), nativeCodingFailures(attempts))
	}
	for n, attempt := range attempts {
		assertCodingRuntimeStopped(t, attempt)
		_, input, err := e.Store.Artifact(ctx, attempt.InputArtifact)
		requireWorkflowSuccess(t, err)
		var recorded model.AttemptInput
		requireWorkflowSuccess(t, json.Unmarshal(input, &recorded))
		if !slices.Equal(recorded.Policy.AllowedPaths, plan.Plan.Units[0].AllowedPaths) {
			t.Fatalf("attempt %d changed the approved project write scope: %v", n, recorded.Policy.AllowedPaths)
		}
		if n > 0 && (attempts[n-1].FinishedAt == "" || attempt.StartedAt < attempts[n-1].FinishedAt) {
			t.Fatalf("replacement started before prior attempt stopped: %d", n)
		}
	}
	if attempts[0].Provider != nativeCodingFixturePrimary || attempts[3].Provider != nativeCodingFixturePrimary ||
		attempts[4].Provider != "fake" || attempts[4].RequestedSessionID != "" || attempts[4].Success == nil || !*attempts[4].Success {
		t.Fatal("fallback did not start a fresh successful Coding session")
	}
}

func qualifyLiveCodingCandidate(t *testing.T, live model.Provider, choice model.AgentChoice) {
	t.Helper()
	e, plan, project := nativeCodingPlan(t, &live, &choice, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute+15*time.Second)
	defer cancel()
	e.dispatch(ctx)
	e.background.Wait()
	task, _ := localStoredTask(t, e, plan.ID)
	if task.Stage == "repair" {
		t.Logf("independent checks requested Coding repair: %s", task.Error)
		requireWorkflowSuccess(t, e.Step(ctx, plan.ID))
	}
	candidate := assertNativeCodingCandidate(t, e, project, plan)
	attempts := nativeCodingAttempts(t, e, plan)
	last := attempts[len(attempts)-1]
	if last.Provider != choice.Provider || last.ResolvedModel != choice.Model || !reflect.DeepEqual(last.EffectiveThinking, choice.Thinking) {
		t.Fatalf("Codex did not confirm effective Coding selection: provider=%s model=%s thinking=%+v", last.Provider, last.ResolvedModel, last.EffectiveThinking)
	}
	t.Logf("verified Coding candidate commit=%s tree=%s, model=%s thinking=%+v, nono stopped", candidate.CandidateCommit, candidate.CandidateTree, last.ResolvedModel, last.EffectiveThinking)
}

func nativeCodingPlan(t *testing.T, live *model.Provider, choice *model.AgentChoice, fallback bool) (*Engine, model.ExecutionPlan, model.Project) {
	t.Helper()
	e, idea := approvedIdeaPlannerFixture(t)
	return nativeCodingPlanForIdea(t, e, idea, live, choice, fallback)
}

func nativeCodingPlanForIdea(t *testing.T, e *Engine, idea model.Idea, live *model.Provider, choice *model.AgentChoice, fallback bool) (*Engine, model.ExecutionPlan, model.Project) {
	t.Helper()
	draft := pendingCodingPairForIdea(t, e, idea)
	installNativeParityVerifier(t)
	ctx := context.Background()
	project, err := e.Project(ctx, draft.ProjectID)
	requireWorkflowSuccess(t, err)
	configureNativeCodingRepository(t, e, &project)
	configureNativeCodingPeers(t, e, project, live, choice, fallback)

	view, err := e.ReadLocalPlan(ctx, draft.ID)
	requireWorkflowSuccess(t, err)
	spec := *draft.Plan
	spec.Specification = ""
	spec.What = "Print verified from src/result.sh. Add regression coverage and remove src/obsolete.txt."
	spec.Where = []string{nativeCodingSource, nativeCodingTest, nativeCodingDelete}
	spec.Scope = []string{"Edit the result script, add src/test_result.sh, and delete src/obsolete.txt."}
	spec.Exclusions = []string{"Keep every other file unchanged."}
	spec.Steps = []string{"Create src/test_result.sh with `test \"$(sh src/result.sh)\" = verified` and record red via the regression check ID.", "Replace src/result.sh with one line, `printf verified`. Delete src/obsolete.txt and record green via the regression check ID."}
	spec.AcceptanceCriteria = []string{"The result script prints verified, the regression test passes, and obsolete.txt is absent."}
	spec.Requirements = []model.Requirement{{ID: "REQ-RESULT", Outcome: spec.AcceptanceCriteria[0], EvidenceMethod: "Run the committed regression check and inspect the candidate diff."}}
	spec.Units = []model.Unit{{ID: "coding", Objective: strings.Join(spec.Steps, "\n"), AllowedPaths: spec.Where}}
	_, version := localStoredTask(t, e, draft.ID)
	requireWorkflowSuccess(t, e.SaveLocalPlan(ctx, draft.ID, version, view.Hash, RenderPlanDocument(spec)))
	draft, version = localStoredTask(t, e, draft.ID)
	e.dispatch(ctx)
	e.background.Wait()
	for _, attempt := range nativeCodingAttempts(t, e, draft) {
		t.Fatalf("draft scheduled Coding attempt %s", attempt.AttemptID)
	}
	requireWorkflowSuccess(t, e.ReviewLocalPlan(ctx, draft.ID, version, "approve", draft.PlanDocument.Hash, ""))
	approved, _ := localStoredTask(t, e, draft.ID)
	requireWorkflowSuccess(t, os.WriteFile(draft.PlanDocument.Path, []byte("A disk edit must never replace frozen instructions.\n"), 0600))
	return e, approved, project
}

func configureNativeCodingRepository(t *testing.T, e *Engine, project *model.Project) {
	t.Helper()
	root := project.Path
	requireWorkflowSuccess(t, os.Mkdir(filepath.Join(root, "src"), 0700))
	for path, content := range map[string]string{
		resultFile: "protected\n", nativeCodingSource: "printf wrong\n", nativeCodingDelete: "obsolete\n",
		"fixture-check.sh": `if [ ! -f src/test_result.sh ]; then
  printf '%s\n' '{"Package":"fixture","Action":"skip"}'
  exit 0
fi
printf '%s\n' '{"Package":"fixture","Action":"run","Test":"Result"}'
if sh src/test_result.sh; then
  printf '%s\n' '{"Package":"fixture","Action":"pass","Test":"Result"}' '{"Package":"fixture","Action":"pass"}'
else
  printf '%s\n' '{"Package":"fixture","Action":"fail","Test":"Result"}' '{"Package":"fixture","Action":"fail"}'
  exit 1
fi
`,
	} {
		requireWorkflowSuccess(t, os.WriteFile(filepath.Join(root, path), []byte(content), 0600))
	}
	scanner := cleanQltyFixtureCheck(t)
	checks := []model.Check{
		{Name: nativeCodingCheck, Command: []string{"/bin/sh", "fixture-check.sh"}, ReportFormat: "go-test-json", TimeoutSeconds: 10},
		scanner,
	}
	optionalFixtureQuality(t, project, checks)
	project.Policy.Checks = append(project.Policy.Checks, checks...)
	project.Policy.Commands = [][]string{{"/bin/sh", "-c"}}
	raw, err := yaml.Marshal(project.Policy)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, os.WriteFile(filepath.Join(root, ".whirlwind.yaml"), raw, 0600))
	submissionGit(t, root, "add", "--all")
	submissionGit(t, root, "commit", "-m", "Create isolated Coding acceptance fixture")
	project.Policy, project.PolicyHash, err = policy.Load(root)
	requireWorkflowSuccess(t, err)
	ctx := context.Background()
	project.QualityIdentity = qualifyNativeFixtureScanner(t, *project, scanner)
	record, err := e.Store.Get(ctx, "projects", project.ID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.Store.Put(ctx, "projects", project.ID, *project, record.Version))
}

func qualifyNativeFixtureScanner(t *testing.T, project model.Project, scanner model.Check) *model.QualityIdentity {
	t.Helper()
	quality, err := externalQualitySnapshot(project)
	requireWorkflowSuccess(t, err)
	quality.PluginCatalog = []byte("plugins: {}\n")
	head := submissionGit(t, project.Path, nativeCodingFixtureRevParse, "HEAD")
	tree := submissionGit(t, project.Path, nativeCodingFixtureRevParse, nativeCodingFixtureHeadTree)
	const id = "fixture-quality-setup"
	manifest := verification.JobManifest{
		Version: 1, Purpose: verification.ProjectSetupPurpose,
		ProjectID: project.ID, OperationID: id, JobID: id, ConfigurationHash: project.PolicyHash,
		CandidateCommit: head, CandidateTree: tree, BaseCommit: head,
		Repository: project.Path, Root: filepath.Join(quality.Root, "jobs", id),
		Launcher: os.Getenv("WHIRLWIND_EXECUTION_LAUNCHER"), Sandbox: project.Policy.Sandbox, Quality: &quality,
		Commands: []verification.JobCommand{
			{Name: scanner.Name, Command: scanner.Command, Mode: scanner.Mode, ReportFormat: scanner.ReportFormat, TimeoutSeconds: 15},
		},
	}
	report, err := verification.RunLocalJob(context.Background(), os.Getenv("WHIRLWIND_VERIFY_BIN"), manifest, nil)
	requireWorkflowSuccess(t, err)
	if report.QualityIdentity == nil {
		t.Fatalf("fixture quality runner did not qualify: %+v", report.Results)
	}
	return report.QualityIdentity
}

func configureNativeCodingPeers(t *testing.T, e *Engine, project model.Project, live *model.Provider, choice *model.AgentChoice, fallback bool) {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(source), "testdata", "coding-acp-peer.mjs")
	node, err := exec.LookPath("node")
	requireWorkflowSuccess(t, err)
	ctx := context.Background()
	fixture, err := e.Store.Get(ctx, "providers", "fake")
	requireWorkflowSuccess(t, err)
	secondary, err := store.Decode[model.Provider](fixture)
	requireWorkflowSuccess(t, err)
	secondary.Command = []string{node, script, "code", project.Path}
	requireWorkflowSuccess(t, e.Store.Put(ctx, "providers", secondary.ID, secondary, fixture.Version))
	chain := []model.AgentChoice{{Provider: secondary.ID, Model: nativeCodingFixtureModel}}
	if live != nil {
		live.Available, live.Quota = provider.Available(*live), "unknown"
		requireWorkflowSuccess(t, e.Store.Put(ctx, "providers", live.ID, *live, 0))
		chain = []model.AgentChoice{*choice}
	} else if fallback {
		primary := secondary
		primary.ID = nativeCodingFixturePrimary
		primary.Command = []string{node, script, "transport-failure", project.Path}
		requireWorkflowSuccess(t, e.Store.Put(ctx, "providers", primary.ID, primary, 0))
		chain = append([]model.AgentChoice{{Provider: primary.ID, Model: nativeCodingFixtureModel}}, chain...)
	}
	putFixtureAgent(t, e.Store, "code", model.AgentConfig{Role: "code", Chain: chain})
	for _, role := range []string{"review", "finalizer"} {
		peer := secondary
		peer.ID = "coding-fixture-" + role
		peer.Command = []string{node, script, role, project.Path}
		requireWorkflowSuccess(t, e.Store.Put(ctx, "providers", peer.ID, peer, 0))
		putFixtureAgent(t, e.Store, role, model.AgentConfig{Role: role, Chain: []model.AgentChoice{{Provider: peer.ID, Model: nativeCodingFixtureModel}}})
	}
	e.Adapter = func(selected model.Provider) provider.Adapter { return provider.ProcessAdapter{Provider: selected} }
}

func assertNativeCodingCandidate(t *testing.T, e *Engine, project model.Project, plan model.ExecutionPlan) verification.JobReport {
	t.Helper()
	ctx := context.Background()
	task, _ := localStoredTask(t, e, plan.ID)
	attempts := nativeCodingAttempts(t, e, task)
	if task.Stage != "review" || !task.ChecksPassed || len(attempts) == 0 {
		source, _ := os.ReadFile(filepath.Join(task.Worktree, nativeCodingSource))
		_, testErr := os.Stat(filepath.Join(task.Worktree, nativeCodingTest))
		_, oldErr := os.Stat(filepath.Join(task.Worktree, nativeCodingDelete))
		t.Fatalf("Coding incomplete: stage=%s error=%q attempts=%v fixture={exact source: %t, regression present: %t, obsolete absent: %t}",
			task.Stage, task.Error, nativeCodingFailures(attempts), string(source) == "printf verified\n", testErr == nil, os.IsNotExist(oldErr))
	}
	last := attempts[len(attempts)-1]
	assertCodingRuntimeStopped(t, last)
	row, err := e.Store.Get(ctx, "verification", task.VerificationID)
	requireWorkflowSuccess(t, err)
	record, err := store.Decode[struct {
		Report verification.JobReport `json:"runner_report"`
		Patch  string                 `json:"patch_artifact"`
	}](row)
	requireWorkflowSuccess(t, err)
	report := record.Report
	if !report.Passed || report.PlanVersion != plan.PlanVersion || report.PlanHash != plan.PlanDocument.ApprovedHash ||
		report.CandidateCommit != submissionGit(t, task.Worktree, nativeCodingFixtureRevParse, "HEAD") ||
		report.CandidateTree != submissionGit(t, task.Worktree, nativeCodingFixtureRevParse, nativeCodingFixtureHeadTree) || record.Patch == "" {
		t.Fatalf("runner output does not identify the exact approved candidate: %+v", report)
	}
	for path, want := range map[string]string{nativeCodingSource: "printf verified", resultFile: "protected"} {
		if got := submissionGit(t, task.Worktree, "show", "HEAD:"+path); got != want {
			t.Fatalf("candidate %s = %q, want %q", path, got, want)
		}
	}
	if paths := submissionGit(t, task.Worktree, "diff", "--name-status", task.Base+"..HEAD"); paths != "D\t"+nativeCodingDelete+"\nM\t"+nativeCodingSource+"\nA\t"+nativeCodingTest {
		t.Fatalf("candidate diff must contain exactly one addition, change and deletion: %q", paths)
	}
	if got := submissionGit(t, project.Path, "status", "--porcelain"); got != "" {
		t.Fatalf("Coding dirtied the primary worktree: %q", got)
	}
	checks, err := e.Store.List(ctx, "tdd_checks")
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.requireTDDEvidence(ctx, task))
	var phases []string
	for _, row := range checks {
		check, err := store.Decode[tddCheck](row)
		requireWorkflowSuccess(t, err)
		if check.Task == task.ID && check.PlanVersion == task.PlanVersion && check.CheckID == nativeCodingCheck && check.OutputArtifact != "" {
			phases = append(phases, check.Phase)
		}
	}
	if slices.Index(phases, "red") < 0 || slices.Index(phases, "green") <= slices.Index(phases, "red") {
		t.Fatalf("Coding did not produce ordered red and green broker evidence: %v", phases)
	}
	return report
}

func assertCodingRuntimeStopped(t *testing.T, attempt model.Attempt) {
	t.Helper()
	proof, exists, err := execution.ReadRuntimeLifecycle(execution.Profile{
		Launcher: attempt.RuntimeLauncher, LifecycleFile: attempt.LifecycleFile, SandboxName: attempt.SandboxName,
		ExecutionID: attempt.ExecutionID, ExecutionGeneration: attempt.ExecutionGeneration,
		ExecutionLabelValue: attempt.ExecutionLabelValue, DeadlineAt: attempt.DeadlineAt,
		GatewayName: attempt.GatewayName, GatewayEndpoint: attempt.GatewayEndpoint, Workspace: attempt.Workspace,
	})
	if attempt.RuntimeKind != "nono" || err != nil || !exists || proof.SandboxPhase != "Stopped" ||
		proof.ActiveProcesses != 0 || proof.SandboxHealth != "stopped" {
		t.Fatalf("Coding did not record clean native shutdown: phase=%s err=%v", proof.SandboxPhase, err)
	}
}

func nativeCodingAttempts(t *testing.T, e *Engine, plan model.ExecutionPlan) []model.Attempt {
	t.Helper()
	rows, err := e.Store.List(context.Background(), "sessions")
	requireWorkflowSuccess(t, err)
	var result []model.Attempt
	for _, row := range rows {
		attempt, err := store.Decode[model.Attempt](row)
		requireWorkflowSuccess(t, err)
		if attempt.Task == plan.ID && attempt.Role == "code" && attempt.StartedAt != "" {
			result = append(result, attempt)
		}
	}
	slices.SortFunc(result, func(a, b model.Attempt) int { return strings.Compare(a.StartedAt, b.StartedAt) })
	return result
}

func nativeCodingFailures(attempts []model.Attempt) []string {
	results := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		results = append(results, fmt.Sprintf("%s/%s: %s %s", attempt.Provider, attempt.Model, attempt.Status, attempt.Error))
	}
	return results
}
