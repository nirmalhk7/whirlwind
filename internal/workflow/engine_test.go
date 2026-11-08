package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
	"whirlwind/internal/verification"
)

const resultFile = "result.txt"

type fakeGitHub struct{}

// Issues returns no remote issue inventory for workflow fixtures.
func (fakeGitHub) Issues(context.Context, string) ([]model.Issue, error) { return nil, nil }

// Issue returns an empty remote issue snapshot without contacting GitHub.
func (fakeGitHub) Issue(context.Context, string, int) (model.Issue, error) { return model.Issue{}, nil }

// Sources returns empty ideation sources without contacting GitHub.
func (fakeGitHub) Sources(context.Context, string) (any, error) { return map[string]any{}, nil }

// CreateIssue returns a fixed open, labeled issue to simulate plan publication.
func (fakeGitHub) CreateIssue(context.Context, string, string, string, string) (model.Issue, error) {
	return model.Issue{Number: 1, State: "OPEN", Labels: []string{"whirlwind:ready"}, UpdatedAt: "1"}, nil
}

// CreatePR returns a fixed PR URL without pushing or publishing remotely.
func (fakeGitHub) CreatePR(_ context.Context, _, _, _, _, _, _ string) (string, error) {
	return "https://github.com/o/r/pull/1", nil
}

// RegisterWebhook accepts fixture webhook registration without a remote write.
func (fakeGitHub) RegisterWebhook(context.Context, string, string, string) error { return nil }

// PRState keeps fixture PRs open so reconciliation does not complete them.
func (fakeGitHub) PRState(context.Context, string, string) (string, error) { return "OPEN", nil }

type recordingGitHub struct {
	fakeGitHub
	prBody string
}

// CreatePR captures the PR body for assertions and returns the fixture publication result.
func (g *recordingGitHub) CreatePR(_ context.Context, _, _, _, _, _, body string) (string, error) {
	g.prBody = body
	return "https://github.com/o/r/pull/1", nil
}

type fakeAgent struct{ score float64 }

// Run returns a fixture plan or review, or writes the expected coding result inside the supplied worktree.
func (f fakeAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	switch in.Role {
	case "plan":
		b, _ := json.Marshal(model.Plan{
			Objective: "Add file", What: "Add result.txt", Why: "Result is missing", Benefit: "Result becomes available", Where: []string{resultFile},
			Scope: []string{"Create the result file in the approved worktree."}, Exclusions: []string{"Do not change unrelated files."}, Steps: []string{"Add a failing result check.", "Create result.txt.", "Run the configured result check."},
			AcceptanceCriteria: []string{"file exists"}, Requirements: []model.Requirement{{ID: "REQ-RESULT", Outcome: "result.txt exists in the candidate worktree.", EvidenceMethod: "Run the configured test -f result.txt check."}},
			RepositoryTestCommands: [][]string{{"sh", "-c", "test -f result.txt"}}, RepositoryTestInventory: "Configured: .whirlwind.yaml checks.test: sh -c 'test -f result.txt'", RepositoryBuildInventory: "None configured: .whirlwind.yaml inspected.",
			Units: []model.Unit{{ID: "one", Objective: "add result.txt", AllowedPaths: []string{resultFile}}},
		})
		return provider.Output{Text: string(b), SessionID: "planner"}, nil
	case "code":
		if e := os.WriteFile(filepath.Join(in.CWD, resultFile), []byte("verified\n"), 0600); e != nil {
			return provider.Output{}, e
		}
		return provider.Output{Text: "done", SessionID: "coder"}, nil
	case "submit":
		b, _ := json.Marshal(map[string]any{"title": "Improve result", "body": "Makes the result available. Validation passed.", "commits": []map[string]any{{"message": "feat: add verified result", "files": []string{resultFile}}}})
		return provider.Output{Text: string(b), SessionID: "submitter"}, nil
	case "review":
		b, _ := json.Marshal(model.Review{Score: f.score, Findings: []model.Finding{}, Summary: "ok"})
		return provider.Output{Text: string(b)}, nil
	}
	return provider.Output{}, nil
}

// testEngine creates a committed temporary repository, real SQLite store, fake provider, and eligible task for workflow tests.
func testEngine(t *testing.T) (*Engine, model.Task, string) {
	ctx := context.Background()
	if os.Getenv("WHIRLWIND_OPENSHELL_CLI") == "" {
		fixture := openshell.Install(t)
		t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	}
	if os.Getenv("WHIRLWIND_OPENSHELL_IMAGE") == "" {
		t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "test-image")
	}
	if os.Getenv("WHIRLWIND_OPENSHELL_PROXY_TOKEN") == "" {
		t.Setenv("WHIRLWIND_OPENSHELL_PROXY_TOKEN", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%s %v", b, e)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, ".whirlwind.yaml"), []byte("budget:\n  max_attempts: 100\n  max_runtime_seconds: 3600\n  verification_attempts: 4\n  verification_runtime_seconds: 60\nchecks:\n  - name: test\n    command: [sh, -c, 'test -f result.txt']\nallowed_paths: ['**']\nrisk_rules:\n  - level: high\n    paths: ['auth/**']\ncommands: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")
	p, hash, e := policy.Load(repo)
	if e != nil {
		t.Fatal(e)
	}
	s, e := store.Open(filepath.Join(root, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	settings := DefaultSettings()
	settings.Admission = model.AdmissionSettings{DiscoveryBudget: &model.BudgetPolicy{MaxAttempts: 100, MaxRuntimeSeconds: 3600}, DefaultBudget: &model.BudgetPolicy{MaxAttempts: 100, MaxRuntimeSeconds: 3600, VerificationAttempts: 4, VerificationRuntimeSeconds: 60}, Pools: map[string]model.QuotaReserve{"*": {MinRemainingPercent: 0, MaxAgeSeconds: 60, MaxConcurrent: 8}}}
	if err := s.Put(ctx, "settings", "global", settings, 0); err != nil {
		t.Fatal(err)
	}
	project := model.Project{ID: "p", Repo: "o/r", Path: repo, Policy: p, PolicyHash: hash}
	if err := s.Put(ctx, "projects", "p", project, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "providers", "fake", model.Provider{ID: "fake", Kind: "acp", Available: true, Quota: "unknown", Capabilities: model.AgentRoles()}, 0); err != nil {
		t.Fatal(err)
	}
	installFixtureAgentQueues(t, s)
	task, e := s.IngestIssue(ctx, project, model.Issue{Number: 1, Title: "file", Body: "add file", State: "OPEN", Labels: []string{"whirlwind:ready"}})
	if e != nil {
		t.Fatal(e)
	}
	engine := New(s, fakeGitHub{}, root)
	engine.PlansRoot = filepath.Join(root, "plans")
	// Most workflow fixtures represent already selected work; intake gates have dedicated tests.
	task.Selection = &model.SelectionAuthorization{CandidateID: "fixture-selection", CandidateVersion: 1, Objective: task.Issue.Title, PolicyHash: hash, SelectedAt: "2026-10-22T00:00:00Z"}
	task.Stage = "planning"
	record, _ := s.Get(ctx, "tasks", task.ID)
	if err := s.Put(ctx, "tasks", task.ID, task, record.Version); err != nil {
		t.Fatal(err)
	}
	candidates, _ := s.List(ctx, "directions")
	for _, candidate := range candidates {
		if err := s.RemoveDirection(ctx, candidate.ID, candidate.Version); err != nil {
			t.Fatal(err)
		}
	}
	engine.Adapter = func(model.Provider) provider.Adapter { return fakeAgent{9} }
	return engine, task, repo
}

func installFixtureAgentQueues(t *testing.T, s *store.Store) {
	t.Helper()
	for _, role := range model.AgentRoles() {
		config := model.AgentConfig{Role: role, Chain: []model.AgentChoice{{Provider: "fake", Model: "fixture-model"}}}
		putFixtureAgent(t, s, role, config)
	}
}

func putFixtureAgent(t *testing.T, s *store.Store, role string, value any) {
	t.Helper()
	record, err := s.Get(context.Background(), "agents", role)
	expected := 0
	if err == nil {
		expected = record.Version
	} else if err != store.ErrNotFound {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), "agents", role, value, expected); err != nil {
		t.Fatalf("save explicit %s fixture queue: %v", role, err)
	}
}

// TestIssueToPRUsesWorktreeAndVerification verifies an explicitly approved, verified candidate reaches local delivery without changing its source checkout.
func TestIssueToPRUsesWorktreeAndVerification(t *testing.T) {
	ctx := context.Background()
	e, task, project, record := acceptedDeliveryFixture(t)
	sourceHead := submissionGit(t, project.Path, "rev-parse", "HEAD")
	sourceStatus := submissionGit(t, project.Path, "status", "--porcelain")
	done := completeAcceptedLocalDeliveryFixture(t, e, task, project, record)
	if done.Stage != "complete" || done.Delivery == nil || done.Delivery.Stage != "complete" || !done.ChecksPassed || done.PR != "" {
		t.Fatalf("approved candidate did not reach local delivery: %+v", done)
	}
	if done.Delivery.Worktree == project.Path || done.Delivery.ReviewedCommit != task.ReviewedCommit {
		t.Fatalf("local delivery is not an isolated copy of the accepted candidate: %+v", done.Delivery)
	}
	if sourceHeadAfter := submissionGit(t, project.Path, "rev-parse", "HEAD"); sourceHeadAfter != sourceHead {
		t.Fatalf("local delivery changed source checkout HEAD: before=%s after=%s", sourceHead, sourceHeadAfter)
	}
	if sourceStatusAfter := submissionGit(t, project.Path, "status", "--porcelain"); sourceStatusAfter != sourceStatus {
		t.Fatalf("local delivery changed source checkout files: before=%q after=%q", sourceStatus, sourceStatusAfter)
	}
	publications, err := e.Store.List(ctx, "publications")
	if err != nil || len(publications) != 0 {
		t.Fatalf("local delivery created remote publication state: %d %v", len(publications), err)
	}
}

// TestSelectedDirectionPublishesPlanBeforeCoding verifies a selected idea reaches plan publication before implementation begins.
func TestSelectedDirectionPublishesPlanBeforeCoding(t *testing.T) {
	ctx := context.Background()
	e, original, _ := testEngine(t)
	task := model.Task{ID: "selected", ProjectID: original.ProjectID, Stage: "planning", PlanVersion: 1, Issue: model.Issue{Title: "idea", Body: "add file"}}
	task.Selection = original.Selection
	if err := e.Store.Put(ctx, "tasks", task.ID, task, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	planned, _ := store.Decode[model.Task](r)
	if planned.Stage != "plan_approval" || planned.Worktree != "" || planned.PlanDocument == nil || planned.PlanDocument.ApprovedHash != "" {
		t.Fatalf("coding started before separate local plan approval: %+v", planned)
	}
	view, err := e.ReadLocalPlan(ctx, planned.ID)
	if err != nil || view.Hash == "" || len(view.Plan.Requirements) != 1 || view.Plan.Requirements[0].ID != "REQ-RESULT" {
		t.Fatalf("selected direction did not publish its reviewable specification: %+v %v", view, err)
	}
	if _, err := e.Store.Get(ctx, "plans", planned.ID+":1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unapproved plan requirements were prematurely frozen: %v", err)
	}
}

// TestFailedChecksDoNotStartReview verifies failed required checks send the task to repair rather than AI review.
func TestFailedChecksDoNotStartReview(t *testing.T) {
	fixture := newAgenticReviewFixture(t, true)
	if err := fixture.e.Step(fixture.ctx, fixture.task.ID); err != nil {
		t.Fatal(err)
	}
	blocked, _ := localStoredTask(t, fixture.e, fixture.task.ID)
	if blocked.Stage != "repair" || blocked.ChecksPassed || blocked.ReviewID != "" || blocked.Delivery != nil || blocked.PlanDocument == nil || blocked.PlanDocument.ApprovedHash == "" {
		t.Fatalf("failed configured check reached review or bypassed human plan approval: %+v", blocked)
	}
	plan, err := fixture.e.Store.Get(fixture.ctx, "plans", blocked.ID+":1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan.Data), "REQ-RESULT") {
		t.Fatalf("frozen requirement snapshot missing: %s", plan.Data)
	}
	record, err := fixture.e.Store.Get(fixture.ctx, "verification", blocked.VerificationID)
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
		t.Fatalf("failed configured check evidence not retained: %+v", evidence)
	}
	var failure *verification.Result
	for index := range evidence.Report.Results {
		if evidence.Report.Results[index].Name == "failing" {
			failure = &evidence.Report.Results[index]
			break
		}
	}
	if failure == nil || failure.Passed || failure.ExitCode == nil || *failure.ExitCode == 0 || failure.OutputArtifact == "" || !failure.Runtime.CleanupVerified {
		t.Fatalf("failed check lacks process output and cleanup proof: %+v", failure)
	}
	_, _, err = fixture.e.Store.Artifact(fixture.ctx, failure.OutputArtifact)
	if err != nil {
		t.Fatalf("failed check artifact unavailable: %v", err)
	}
	jobRecord, err := fixture.e.Store.Get(fixture.ctx, "runtime_jobs", failure.Runtime.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[runtimeCheckJob](jobRecord)
	if err != nil || job.Stage != "complete" || !job.CleanupVerified || job.TaskID != blocked.ID || job.VerificationID != blocked.VerificationID || job.Role != "verify" || job.CheckID != "failing" {
		t.Fatalf("failed check runtime owner proof mismatch: job=%+v err=%v", job, err)
	}
	if candidateHead := submissionGit(fixture.t, blocked.Worktree, "rev-parse", "HEAD"); candidateHead == "" {
		t.Fatal("candidate worktree was not preserved")
	}
	fixture.verifyTDD(blocked)
	reviews, err := fixture.e.Store.List(fixture.ctx, "reviews")
	if err != nil {
		t.Fatal(err)
	}
	for _, saved := range reviews {
		review, err := store.Decode[model.Review](saved)
		if err != nil {
			t.Fatal(err)
		}
		if review.Provenance != nil && review.Provenance.Task == blocked.ID {
			t.Fatalf("reviewer started despite failed configured check: %+v", review)
		}
	}
}

type quotaAgent struct{ fail bool }
