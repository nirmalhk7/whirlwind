package workflow

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type ideationGitHubSpy struct {
	fakeGitHub
	sourcesCalls, issueCalls int
	err                      error
	sources                  any
}

func (git *ideationGitHubSpy) Sources(context.Context, string) (any, error) {
	git.sourcesCalls++
	return git.sources, git.err
}

func (git *ideationGitHubSpy) Issues(context.Context, string) ([]model.Issue, error) {
	git.issueCalls++
	return nil, git.err
}

const localIdeationFixtureDay = "2026-11-24"

func TestLocalGitIdeationUsesRepositoryEvidenceWithoutGitHub(t *testing.T) {
	for _, trigger := range []string{"initial", "manual"} {
		t.Run(trigger, func(t *testing.T) {
			fixture := prepareNoveltyTest(t, nil)
			head := configureLocalIdeationProject(t, &fixture)
			spy := &ideationGitHubSpy{err: errors.New(`gh: expected the "[HOST/]OWNER/REPO" format`)}
			fixture.engine.GitHub = spy

			runID := dispatchLocalIdeationFixture(t, fixture, trigger == "initial")
			assertLocalIdeationEvidence(t, fixture, runID, head)
			assertLocalReconciliationRetainsOutage(t, fixture)
			if spy.sourcesCalls != 0 || spy.issueCalls != 0 {
				t.Fatalf("local-only project used remote sources: %+v", spy)
			}
		})
	}
}

// The smoke driver supplies a fresh Basera checkout with no remotes. The
// deterministic adapter proposes only README edits against its committed tree.
func TestBaseraSmokeIdeationUsesCommittedReadme(t *testing.T) {
	source := os.Getenv("WHIRLWIND_TEST_BASERA_CHECKOUT")
	if source == "" {
		t.Skip("run scripts/basera-smoke.py to supply the disposable Basera checkout")
	}
	fixture := prepareNoveltyTest(t, nil)
	repo := filepath.Join(t.TempDir(), "basera")
	cmd := exec.CommandContext(t.Context(), "git", "clone", "--quiet", "--no-local", "--", source, repo)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone disposable Basera snapshot: %v: %s", err, output)
	}
	submissionGit(t, repo, "remote", "remove", "origin")
	fixture.project.Path = repo
	head := configureLocalIdeationProject(t, &fixture)

	topics := []string{"Add a local setup example", "Shorten the startup steps", "Document supported Node versions", "Clarify the README test command"}
	for i := range fixture.fixture.batch {
		direction := &fixture.fixture.batch[i]
		direction.Title = topics[i]
		direction.Description = topics[i] + " in the Basera README."
		direction.Problem = "The Basera README could be clearer about " + strings.ToLower(topics[i]) + "."
		direction.Mechanism = "Update the README wording."
		direction.Components = []string{"README.md"}
		direction.Evidence = []model.EvidenceRef{{Kind: "code", Ref: "README.md"}}
	}
	spy := &ideationGitHubSpy{err: errors.New("the Basera fixture has no GitHub credentials")}
	fixture.engine.GitHub = spy
	runID := dispatchLocalIdeationFixture(t, fixture, false)
	assertLocalIdeationSnapshot(t, fixture, runID, head)
	if commits := fixture.fixture.commits; len(commits) == 0 || commits[0] != head {
		t.Fatalf("adapter did not read the pinned Basera checkout: %v", commits)
	}
	if spy.sourcesCalls != 0 || spy.issueCalls != 0 {
		t.Fatal("the Basera fixture attempted a hosted source read")
	}
	t.Logf("Basera %s: ideation %s saved four README proposals against the committed tree", head, runID)
}

func configureLocalIdeationProject(t *testing.T, fixture *noveltyTestFixture) string {
	t.Helper()
	e, p, ctx := fixture.engine, fixture.project, t.Context()
	p.Name, p.Repo, p.RemoteURL, p.Status = "Local fixture", p.Path, "", "draft"
	record, err := e.Store.Get(ctx, "projects", p.ID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.Store.Put(ctx, "projects", p.ID, p, record.Version))
	remotes, err := github.Run(ctx, p.Path, "git", "remote")
	requireWorkflowSuccess(t, err)
	if remotes != "" {
		t.Fatalf("fixture must not have a remote: %s", remotes)
	}
	head, err := github.Run(ctx, p.Path, "git", "rev-parse", "HEAD")
	requireWorkflowSuccess(t, err)
	fixture.project = p
	return head
}

func queueLocalIdeationFixture(t *testing.T, fixture noveltyTestFixture, initial bool) string {
	t.Helper()
	ctx, db, projectID := t.Context(), fixture.engine.Store, fixture.project.ID
	at := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	if initial {
		work := store.InitialProjectWork(projectID, at)
		requireWorkflowSuccess(t, db.Write(ctx, work, nil))
		for _, change := range work {
			if op, ok := change.Value.(model.Operation); ok && op.Kind == "ideation" {
				return change.ID
			}
		}
		t.Fatal("initial project work omitted the ideation operation")
		return ""
	}
	runID := store.ID()
	requireWorkflowSuccess(t, db.Put(ctx, "operations", runID, model.Operation{
		Kind: "ideation", Subject: projectID, Stage: "queued", StartedAt: at.Format(time.RFC3339),
		Input: map[string]any{"day": localIdeationFixtureDay, "fresh": true},
	}, 0))
	return runID
}

func dispatchLocalIdeationFixture(t *testing.T, fixture noveltyTestFixture, initial bool) string {
	t.Helper()
	runID := queueLocalIdeationFixture(t, fixture, initial)
	e, ctx := fixture.engine, t.Context()
	e.dispatchQueuedIdeation(ctx)
	e.background.Wait()
	if initial {
		waiting, _ := inspectOnboardingIdeation(t, e, runID)
		if waiting.Stage != "queued" || !strings.Contains(waiting.Error, "waiting for project setup") || len(fixture.fixture.generationPrompts) != 0 {
			t.Fatalf("local automatic ideation did not wait for setup: %+v", waiting)
		}
		setOnboardingProjectStatus(t, e, fixture.project, "approved")
		e.dispatchQueuedIdeation(ctx)
		e.background.Wait()
	}
	return runID
}

func assertLocalIdeationEvidence(t *testing.T, fixture noveltyTestFixture, runID, head string) {
	t.Helper()
	prompts := fixture.fixture.generationPrompts
	if len(prompts) != 1 || !strings.Contains(prompts[0], "local_repository_only") {
		t.Fatal("provider did not receive explicit local-only scope")
	}
	assertLocalIdeationSnapshot(t, fixture, runID, head)
}

func assertLocalIdeationSnapshot(t *testing.T, fixture noveltyTestFixture, runID, head string) {
	t.Helper()
	db, ctx := fixture.engine.Store, t.Context()
	run, err := db.Get(ctx, "operations", runID)
	requireWorkflowSuccess(t, err)
	op, err := store.Decode[model.Operation](run)
	requireWorkflowSuccess(t, err)
	if op.Stage != "complete" || op.RepositoryRevision != head {
		t.Fatalf("local ideation did not complete using pinned Git evidence: %+v", op)
	}
	assertLocalIdeationDirections(t, fixture, head)
	saved, err := db.Get(ctx, "discovery_scans", fixture.project.ID)
	requireWorkflowSuccess(t, err)
	discovery, err := store.Decode[discoverySnapshot](saved)
	requireWorkflowSuccess(t, err)
	for _, evidence := range discovery.Evidence {
		if evidence.Kind == "github" {
			t.Fatal("local-only ideation fabricated GitHub evidence")
		}
	}
}

func assertLocalIdeationDirections(t *testing.T, fixture noveltyTestFixture, head string) {
	t.Helper()
	results, err := fixture.engine.Store.List(t.Context(), "directions")
	requireWorkflowSuccess(t, err)
	kinds := map[string]bool{}
	for _, result := range results {
		direction, err := store.Decode[model.Direction](result)
		requireWorkflowSuccess(t, err)
		if direction.ProjectID != fixture.project.ID || direction.Day != localIdeationFixtureDay {
			continue
		}
		kinds[direction.Category] = true
		if direction.RepositoryRevision != head || len(direction.Evidence) == 0 || direction.Evidence[0].Kind != "code" {
			t.Fatalf("local direction did not retain committed source evidence: %+v", direction)
		}
	}
	if len(kinds) != 4 {
		t.Fatalf("wanted four locally grounded directions, got %v", kinds)
	}
}

func assertLocalReconciliationRetainsOutage(t *testing.T, fixture noveltyTestFixture) {
	t.Helper()
	e, ctx := fixture.engine, t.Context()
	const existingOutage = "gh: read: operation timed out"
	requireWorkflowSuccess(t, e.Store.Notify(ctx, "", "github", existingOutage))
	e.reconcileProject(ctx, fixture.project)
	notices, err := e.Store.List(ctx, "inbox")
	requireWorkflowSuccess(t, err)
	for _, notice := range notices {
		value, err := store.Decode[model.Notification](notice)
		requireWorkflowSuccess(t, err)
		if value.Message == existingOutage && value.Resolved {
			t.Fatal("local reconciliation falsely cleared an outage without a successful GitHub read")
		}
	}
}

func TestHostedIdeationKeepsGitHubFailuresVisible(t *testing.T) {
	fixture := prepareNoveltyTest(t, nil)
	failure := errors.New("gh: read: repository temporarily unavailable")
	spy := &ideationGitHubSpy{err: failure}
	fixture.engine.GitHub = spy
	err := fixture.engine.Ideate(t.Context(), fixture.project, localIdeationFixtureDay)
	if !errors.Is(err, failure) || spy.sourcesCalls != 1 || fixture.fixture.generations != 0 {
		t.Fatalf("hosted repo silently fell back to local-only context: error=%v calls=%d generated=%d", err, spy.sourcesCalls, fixture.fixture.generations)
	}
}

func TestHostedDiscoveryStillCollectsGitHubEvidence(t *testing.T) {
	fixture := prepareNoveltyTest(t, nil)
	const title = "Hosted issue context marker"
	spy := &ideationGitHubSpy{sources: map[string]any{
		"issues": []map[string]any{{"number": 42, "title": title}},
	}}
	fixture.engine.GitHub = spy
	prompt, _, snapshot, _, err := fixture.engine.prepareDiscovery(t.Context(), fixture.project)
	requireWorkflowSuccess(t, err)
	if spy.sourcesCalls != 1 || !strings.Contains(prompt, title) {
		t.Fatal("hosted discovery skipped its registered GitHub context")
	}
	for _, evidence := range snapshot.Evidence {
		if evidence.Kind == "github" && evidence.Ref == fixture.project.Repo && evidence.Revision != "" {
			return
		}
	}
	t.Fatal("hosted discovery omitted the GitHub evidence reference")
}
