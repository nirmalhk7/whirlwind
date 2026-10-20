package workflow

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
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
	return model.Issue{Number: 1, State: "OPEN", Labels: []string{"whirlwind:plan"}, UpdatedAt: "1"}, nil
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
		b, _ := json.Marshal(model.Plan{Objective: "Add file", AcceptanceCriteria: []string{"file exists"}, Units: []model.Unit{{ID: "one", Objective: "add result.txt"}}})
		return provider.Output{Text: string(b), SessionID: "planner"}, nil
	case "code":
		if e := os.WriteFile(filepath.Join(in.CWD, resultFile), []byte("verified\n"), 0600); e != nil {
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
	if err := os.WriteFile(filepath.Join(repo, ".whirlwind.yaml"), []byte("checks:\n  - name: test\n    command: [sh, -c, 'test -f result.txt']\nallowed_paths: ['**']\nrisk_rules:\n  - level: high\n    paths: ['auth/**']\ncommands: []\n"), 0600); err != nil {
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
	project := model.Project{ID: "p", Repo: "o/r", Path: repo, Policy: p, PolicyHash: hash}
	if err := s.Put(ctx, "projects", "p", project, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "providers", "fake", model.Provider{ID: "fake", Kind: "acp", Available: true, Quota: "unknown", Capabilities: []string{"plan", "code", "review"}}, 0); err != nil {
		t.Fatal(err)
	}
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
	if _, e = os.Stat(filepath.Join(repo, resultFile)); !os.IsNotExist(e) {
		t.Fatal("original checkout modified")
	}
	if _, e = os.Stat(filepath.Join(done.Worktree, resultFile)); e != nil {
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
	if err := e.Store.Put(ctx, "projects", p.ID, p, r.Version); err != nil {
		t.Fatal(err)
	}
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
