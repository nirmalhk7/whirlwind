package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type fakeGitHub struct{}

// Issues returns no remote issue inventory for workflow fixtures.
func (fakeGitHub) Issues(context.Context, string) ([]model.Issue, error) { return nil, nil }

// Issue returns an empty remote issue snapshot without contacting GitHub.
func (fakeGitHub) Issue(context.Context, string, int) (model.Issue, error) { return model.Issue{}, nil }

// Sources returns empty ideation sources without contacting GitHub.
func (fakeGitHub) Sources(context.Context, string) (any, error) { return map[string]any{}, nil }

// CreateIssue returns a fixed open, labeled issue to simulate plan publication.
func (fakeGitHub) CreateIssue(context.Context, string, string, string, string) (model.Issue, error) {
	return model.Issue{Number: 1, State: "OPEN", Labels: []string{"whirlwind:plan"}, UpdatedAt: "1"}, nil
}

// CreatePR returns a fixed PR URL without pushing or publishing remotely.
func (fakeGitHub) CreatePR(context.Context, string, string, string, string, string, string) (string, error) {
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
func (g *recordingGitHub) CreatePR(ctx context.Context, cwd, repo, branch, base, title, body string) (string, error) {
	g.prBody = body
	return g.fakeGitHub.CreatePR(ctx, cwd, repo, branch, base, title, body)
}

type fakeAgent struct{ score float64 }

// Run returns a fixture plan or review, or writes the expected coding result inside the supplied worktree.
func (f fakeAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	switch in.Role {
	case "plan":
		b, _ := json.Marshal(model.Plan{Objective: "Add file", AcceptanceCriteria: []string{"file exists"}, Units: []model.Unit{{ID: "one", Objective: "add result.txt"}}})
		return provider.Output{Text: string(b), SessionID: "planner"}, nil
	case "code":
		if e := os.WriteFile(filepath.Join(in.CWD, "result.txt"), []byte("verified\n"), 0600); e != nil {
			return provider.Output{}, e
		}
		return provider.Output{Text: "done", SessionID: "coder"}, nil
	case "review":
		b, _ := json.Marshal(model.Review{Score: f.score, Findings: []model.Finding{}, Summary: "ok"})
		return provider.Output{Text: string(b)}, nil
	}
	return provider.Output{}, nil
}

// testEngine creates a committed temporary repository, real SQLite store, fake provider, and eligible task for workflow tests.
func testEngine(t *testing.T) (*Engine, model.Task, string) {
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	os.Mkdir(repo, 0700)
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
	os.WriteFile(filepath.Join(repo, ".whirlwind.yaml"), []byte("checks:\n  - name: test\n    command: [sh, -c, 'test -f result.txt']\nallowed_paths: ['**']\nrisk_rules:\n  - level: high\n    paths: ['auth/**']\ncommands: []\n"), 0600)
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
	t.Cleanup(func() { s.Close() })
	project := model.Project{ID: "p", Repo: "o/r", Path: repo, Policy: p, PolicyHash: hash}
	s.Put(ctx, "projects", "p", project, 0)
	s.Put(ctx, "providers", "fake", model.Provider{ID: "fake", Kind: "acp", Available: true, Quota: "unknown", Capabilities: []string{"plan", "code", "review"}}, 0)
	task, e := s.IngestIssue(ctx, project, model.Issue{Number: 1, Title: "file", Body: "add file", State: "OPEN", Labels: []string{"whirlwind:plan"}})
	if e != nil {
		t.Fatal(e)
	}
	engine := New(s, fakeGitHub{}, root)
	engine.Adapter = func(model.Provider) provider.Adapter { return fakeAgent{9} }
	return engine, task, repo
}

// TestIssueToPRUsesWorktreeAndVerification verifies the issue-to-PR workflow passes checks in an isolated worktree without editing the original checkout.
func TestIssueToPRUsesWorktreeAndVerification(t *testing.T) {
	ctx := context.Background()
	engine, task, repo := testEngine(t)
	s := engine.Store
	var e error
	for i := 0; i < 4; i++ {
		if e = engine.Step(ctx, task.ID); e != nil {
			t.Fatal(e)
		}
	}
	r, e := s.Get(ctx, "tasks", task.ID)
	if e != nil {
		t.Fatal(e)
	}
	done, e := store.Decode[model.Task](r)
	if e != nil || done.Stage != "pr_created" || done.PR == "" || !done.ChecksPassed {
		t.Fatalf("%+v %v", done, e)
	}
	if _, e = os.Stat(filepath.Join(repo, "result.txt")); !os.IsNotExist(e) {
		t.Fatal("original checkout modified")
	}
	if _, e = os.Stat(filepath.Join(done.Worktree, "result.txt")); e != nil {
		t.Fatal(e)
	}
}

// TestSelectedDirectionPublishesPlanBeforeCoding verifies a selected idea reaches plan publication before implementation begins.
func TestSelectedDirectionPublishesPlanBeforeCoding(t *testing.T) {
	ctx := context.Background()
	e, original, _ := testEngine(t)
	task := model.Task{ID: "selected", ProjectID: original.ProjectID, Stage: "planning", PlanVersion: 1, Issue: model.Issue{Title: "idea", Body: "add file"}}
	if err := e.Store.Put(ctx, "tasks", task.ID, task, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	planned, _ := store.Decode[model.Task](r)
	if planned.Stage != "plan_ready" || planned.Worktree != "" {
		t.Fatalf("coding before issue publication: %+v", planned)
	}
}

// TestFailedChecksDoNotStartReview verifies failed required checks send the task to repair rather than AI review.
func TestFailedChecksDoNotStartReview(t *testing.T) {
	ctx := context.Background()
	e, task, _ := testEngine(t)
	e.Adapter = func(model.Provider) provider.Adapter { return fakeAgent{9} }
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	p, _ := e.Project(ctx, task.ProjectID)
	p.Policy.Checks = []model.Check{{Name: "failing", Command: []string{"false"}}}
	r, _ := e.Store.Get(ctx, "projects", p.ID)
	e.Store.Put(ctx, "projects", p.ID, p, r.Version)
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Store.Get(ctx, "tasks", task.ID)
	done, _ := store.Decode[model.Task](r)
	if done.Stage != "repair" || done.ChecksPassed {
		t.Fatalf("failed checks reached review: %+v", done)
	}
}

type quotaAgent struct{ fail bool }

// Run simulates either quota exhaustion or a successful continuation by a backup provider.
func (a quotaAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	if a.fail {
		return provider.Output{}, fmt.Errorf("quota exhausted")
	}
	return provider.Output{Text: "continued"}, nil
}

// TestQuotaFailureHandsOffToAnotherProvider verifies quota exhaustion switches providers while continuing the same workspace.
func TestQuotaFailureHandsOffToAnotherProvider(t *testing.T) {
	ctx := context.Background()
	e, task, repo := testEngine(t)
	p, _ := e.Project(ctx, task.ProjectID)
	p.Policy.Providers = []string{"fake", "backup"}
	e.Store.Put(ctx, "providers", "backup", model.Provider{ID: "backup", Available: true, Quota: "unknown", Capabilities: []string{"code"}}, 0)
	e.Adapter = func(p model.Provider) provider.Adapter { return quotaAgent{fail: p.ID == "fake"} }
	out, err := e.agent(ctx, p, task, "code", repo, "continue", false, "")
	if err != nil || out.Text != "continued" {
		t.Fatalf("handoff: %+v %v", out, err)
	}
}

type unitAgent struct{}

// Run writes and commits multiple fixture files to exercise integration of every unit commit.
func (unitAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	unit := filepath.Base(in.CWD)
	if in.Role == "code" {
		for _, suffix := range []string{"one", "two"} {
			file := unit + "-" + suffix + ".txt"
			if err := os.WriteFile(filepath.Join(in.CWD, file), []byte(file), 0600); err != nil {
				return provider.Output{}, err
			}
			for _, args := range [][]string{{"add", file}, {"commit", "-m", file}} {
				if b, err := exec.Command("git", append([]string{"-C", in.CWD}, args...)...).CombinedOutput(); err != nil {
					return provider.Output{}, fmt.Errorf("%s: %w", b, err)
				}
			}
		}
		os.WriteFile(filepath.Join(in.CWD, "result.txt"), []byte("ok"), 0600)
	}
	return provider.Output{Text: "done"}, nil
}

// TestParallelUnitsIntegrateAllCommits verifies serial integration retains every commit produced by independent parallel units.
func TestParallelUnitsIntegrateAllCommits(t *testing.T) {
	ctx := context.Background()
	e, task, _ := testEngine(t)
	task.Stage = "coding"
	task.Plan = &model.Plan{Objective: "two units", AcceptanceCriteria: []string{"both complete"}, Units: []model.Unit{{ID: "a", Objective: "first"}, {ID: "b", Objective: "second"}}}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	e.Store.Put(ctx, "tasks", task.ID, task, r.Version)
	e.Adapter = func(model.Provider) provider.Adapter { return unitAgent{} }
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Store.Get(ctx, "tasks", task.ID)
	done, _ := store.Decode[model.Task](r)
	for _, name := range []string{"a-one.txt", "a-two.txt", "b-one.txt", "b-two.txt"} {
		if _, err := os.Stat(filepath.Join(done.Worktree, name)); err != nil {
			t.Fatalf("lost unit commit %s: %v", name, err)
		}
	}
	if done.Stage != "review" || !done.ChecksPassed {
		t.Fatalf("%+v", done)
	}
}

// TestCriticalChangeWaitsForHumanBeforePR verifies critical-risk acceptance pauses for human approval before publication.
func TestCriticalChangeWaitsForHumanBeforePR(t *testing.T) {
	ctx := context.Background()
	e, task, _ := testEngine(t)
	p, _ := e.Project(ctx, task.ProjectID)
	p.Policy.RiskRules = []model.RiskRule{{Level: "critical", Paths: []string{"result.txt"}}}
	r, _ := e.Store.Get(ctx, "projects", p.ID)
	e.Store.Put(ctx, "projects", p.ID, p, r.Version)
	for n := 0; n < 3; n++ {
		if err := e.Step(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	r, _ = e.Store.Get(ctx, "tasks", task.ID)
	done, _ := store.Decode[model.Task](r)
	if done.Stage != "approval" || done.PR != "" || done.Approved {
		t.Fatalf("approval bypass: %+v", done)
	}
}

// TestProductiveReviewHasNoPassCap verifies a task can be accepted after more than one hundred review passes.
func TestProductiveReviewHasNoPassCap(t *testing.T) {
	ctx := context.Background()
	e, task, _ := testEngine(t)
	for n := 0; n < 2; n++ {
		if err := e.Step(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	done, _ := store.Decode[model.Task](r)
	done.ReviewPasses = 100
	if err := e.Store.Put(ctx, "tasks", task.ID, done, r.Version); err != nil {
		t.Fatal(err)
	}
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Store.Get(ctx, "tasks", task.ID)
	done, _ = store.Decode[model.Task](r)
	if done.Stage != "publishing" || done.ReviewPasses != 101 {
		t.Fatalf("review cap remains: %+v", done)
	}
}

// TestPRDescriptionListsEachUnitOnce verifies the published PR description lists each implementation unit once.
func TestPRDescriptionListsEachUnitOnce(t *testing.T) {
	ctx := context.Background()
	e, task, _ := testEngine(t)
	recorded := &recordingGitHub{}
	e.GitHub = recorded
	for n := 0; n < 4; n++ {
		if err := e.Step(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(recorded.prBody, "- add result.txt"); got != 1 {
		t.Fatalf("PR description lists implementation unit %d times; want once:\n%s", got, recorded.prBody)
	}
}

// TestRepeatedUnchangedFailuresEscalateAfterThreeRecoveries verifies repeated unchanged diffs exhaust three recoveries and pause the task.
func TestRepeatedUnchangedFailuresEscalateAfterThreeRecoveries(t *testing.T) {
	ctx := context.Background()
	e, task, repo := testEngine(t)
	task.Worktree = repo
	task.Base = "HEAD"
	task.Stage = "repair"
	for n := 0; n < 15; n++ {
		if err := e.progress(ctx, &task); err != nil {
			t.Fatal(err)
		}
	}
	if task.Stage != "paused" || task.Recoveries != 3 {
		t.Fatalf("stall not escalated: %+v", task)
	}
}

type permissionAgent struct{}

// Run requests write permission using the canonical worktree path to exercise alias handling.
func (permissionAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	canonical, err := filepath.EvalSymlinks(in.CWD)
	if err != nil {
		return provider.Output{}, err
	}
	allowed, err := h.Permission(ctx, provider.Permission{Tool: "fs/write_text_file", Paths: []string{filepath.Join(canonical, "result.txt")}})
	if err != nil {
		return provider.Output{}, err
	}
	if !allowed {
		return provider.Output{}, fmt.Errorf("configured file permission denied")
	}
	return provider.Output{Text: "allowed"}, nil
}

// TestPermissionNormalizesWorktreeAliases verifies workspace aliases resolve to the canonical path before permission checks.
func TestPermissionNormalizesWorktreeAliases(t *testing.T) {
	e, task, repo := testEngine(t)
	p, _ := e.Project(context.Background(), task.ProjectID)
	e.Adapter = func(model.Provider) provider.Adapter { return permissionAgent{} }
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, err := e.agent(ctx, p, task, "code", repo, "test", false, "")
	if err != nil || out.Text != "allowed" {
		t.Fatalf("alias permission: %+v %v", out, err)
	}
}

// TestInterruptedIntegrationUsesDurableUnitCheckpoints verifies restarting coding restores unit checkpoints without duplicating integrated commits.
func TestInterruptedIntegrationUsesDurableUnitCheckpoints(t *testing.T) {
	ctx := context.Background()
	e, task, _ := testEngine(t)
	for n := 0; n < 2; n++ {
		if err := e.Step(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	done, _ := store.Decode[model.Task](r)
	before, err := github.Run(ctx, done.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	task.Stage = "coding"
	task.Plan = done.Plan
	for n := range task.Plan.Units {
		task.Plan.Units[n].Stage = ""
	}
	if err = e.Store.Put(ctx, "tasks", task.ID, task, r.Version); err != nil {
		t.Fatal(err)
	}
	if err = e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Store.Get(ctx, "tasks", task.ID)
	restored, _ := store.Decode[model.Task](r)
	after, err := github.Run(ctx, restored.Worktree, "git", "rev-parse", "HEAD")
	if err != nil || before != after || restored.Stage != "review" {
		t.Fatalf("checkpoint replay changed commits: %s %s %+v %v", before, after, restored, err)
	}
}

type waitingAgent struct{}

// Run blocks until context cancellation and returns its error to exercise run timeouts.
func (waitingAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	<-ctx.Done()
	return provider.Output{}, ctx.Err()
}

// TestConfiguredRunBudgetStopsAttempt verifies an explicit project run timeout cancels an agent attempt.
func TestConfiguredRunBudgetStopsAttempt(t *testing.T) {
	e, task, repo := testEngine(t)
	p, _ := e.Project(context.Background(), task.ProjectID)
	p.Policy.RunTimeoutSeconds = 1
	e.Adapter = func(model.Provider) provider.Adapter { return waitingAgent{} }
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	_, err := e.agent(ctx, p, task, "code", repo, "test", false, "")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("run budget not enforced: %v %s", err, time.Since(start))
	}
}
