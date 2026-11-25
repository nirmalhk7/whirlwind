package workflow

import (
	"context"
	"errors"
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

func TestLocalGitIdeationUsesRepositoryEvidenceWithoutGitHub(t *testing.T) {
	for _, trigger := range []string{"initial", "manual"} {
		t.Run(trigger, func(t *testing.T) {
			fixture := prepareNoveltyTest(t, nil)
			e, p := fixture.engine, fixture.project
			ctx := t.Context()
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
			spy := &ideationGitHubSpy{err: errors.New(`gh: expected the "[HOST/]OWNER/REPO" format`)}
			e.GitHub = spy
			at := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
			var runID string
			if trigger == "initial" {
				work := store.InitialProjectWork(p.ID, at)
				requireWorkflowSuccess(t, e.Store.Write(ctx, work, nil))
				for _, change := range work {
					if op, ok := change.Value.(model.Operation); ok && op.Kind == "ideation" {
						runID = change.ID
					}
				}
			} else {
				runID = store.ID()
				requireWorkflowSuccess(t, e.Store.Put(ctx, "operations", runID, model.Operation{
					Kind: "ideation", Subject: p.ID, Stage: "queued", StartedAt: at.Format(time.RFC3339),
					Input: map[string]any{"day": "2026-11-24", "fresh": true},
				}, 0))
			}
			e.dispatchQueuedIdeation(ctx)
			e.background.Wait()
			if trigger == "initial" {
				waiting, _ := inspectOnboardingIdeation(t, e, runID)
				if waiting.Stage != "queued" || !strings.Contains(waiting.Error, "waiting for project setup") || len(fixture.fixture.generationPrompts) != 0 {
					t.Fatalf("local automatic ideation did not wait for setup: %+v", waiting)
				}
				setOnboardingProjectStatus(t, e, p, "approved")
				e.dispatchQueuedIdeation(ctx)
				e.background.Wait()
			}
			run, err := e.Store.Get(ctx, "operations", runID)
			requireWorkflowSuccess(t, err)
			op, err := store.Decode[model.Operation](run)
			requireWorkflowSuccess(t, err)
			if op.Stage != "complete" || op.RepositoryRevision != head {
				t.Fatalf("local ideation did not complete using pinned Git evidence: %+v", op)
			}
			if len(fixture.fixture.generationPrompts) != 1 || !strings.Contains(fixture.fixture.generationPrompts[0], "local_repository_only") {
				t.Fatal("provider did not receive explicit local-only scope")
			}
			results, err := e.Store.List(ctx, "directions")
			requireWorkflowSuccess(t, err)
			kinds := map[string]bool{}
			for _, result := range results {
				direction, err := store.Decode[model.Direction](result)
				requireWorkflowSuccess(t, err)
				if direction.ProjectID != p.ID || direction.Day != "2026-11-24" {
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
			saved, err := e.Store.Get(ctx, "discovery_scans", p.ID)
			requireWorkflowSuccess(t, err)
			discovery, err := store.Decode[discoverySnapshot](saved)
			requireWorkflowSuccess(t, err)
			for _, evidence := range discovery.Evidence {
				if evidence.Kind == "github" {
					t.Fatal("local-only ideation fabricated GitHub evidence")
				}
			}
			const existingOutage = "gh: read: operation timed out"
			requireWorkflowSuccess(t, e.Store.Notify(ctx, "", "github", existingOutage))
			e.reconcileProject(ctx, p)
			notices, err := e.Store.List(ctx, "inbox")
			requireWorkflowSuccess(t, err)
			for _, notice := range notices {
				value, err := store.Decode[model.Notification](notice)
				requireWorkflowSuccess(t, err)
				if value.Message == existingOutage && value.Resolved {
					t.Fatal("local reconciliation falsely cleared an outage without a successful GitHub read")
				}
			}
			if spy.sourcesCalls != 0 || spy.issueCalls != 0 {
				t.Fatalf("local-only project used remote sources: %+v", spy)
			}
		})
	}
}

func TestHostedIdeationKeepsGitHubFailuresVisible(t *testing.T) {
	fixture := prepareNoveltyTest(t, nil)
	failure := errors.New("gh: read: repository temporarily unavailable")
	spy := &ideationGitHubSpy{err: failure}
	fixture.engine.GitHub = spy
	err := fixture.engine.Ideate(t.Context(), fixture.project, "2026-11-24")
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
