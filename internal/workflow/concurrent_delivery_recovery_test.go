package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	concurrentDeliveryFixtureCandidateFile = "candidate.txt"
)

const secondCandidateBody = "independently reviewed second candidate\n"

// Portable coverage of interleaved deliveries after accepted evidence. The
// same-repository worktrees, refs, SQLite restart and supervisor step handlers
// are real; the verifier reports and finalizer text are fixtures.
func TestConcurrentAcceptedDeliveriesIsolatePausedPlanAndReconcileAfterRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	engine, first, project, _ := acceptedDeliveryFixture(t)
	second := secondAcceptedDeliveryCandidate(t, ctx, engine, first, project)
	assertPortableReviewAccepted(t, ctx, engine, first)
	assertPortableReviewAccepted(t, ctx, engine, second)
	assertCrossPlanEvidenceRejected(t, ctx, engine, first, second, project)

	_, firstVersion := localStoredTask(t, engine, first.ID)
	requireWorkflowSuccess(t, engine.SetPlanPaused(ctx, first.ID, firstVersion, true))
	paused, pausedVersion := localStoredTask(t, engine, first.ID)
	if paused.Stage != "paused" || paused.ResumeStage != "submitting" || paused.Delivery != nil {
		t.Fatalf("unexpected checkpoint before restart: %+v", paused)
	}
	mainBefore := submissionGit(t, project.Path, gitRevParseCommand, "refs/heads/main")
	requireWorkflowSuccess(t, engine.Step(ctx, second.ID))
	prepared, _ := localStoredTask(t, engine, second.ID)
	if prepared.Delivery == nil || prepared.Delivery.Stage != "prepared" {
		t.Fatalf("peer did not advance while first was paused: %+v", prepared.Delivery)
	}
	// Simulate loss between the Git ref write and the recorded checkpoint:
	// the owned branch exists, but the finalizer worktree does not.
	ref := "refs/heads/" + prepared.Delivery.Branch
	submissionGit(t, project.Path, "update-ref", ref, prepared.ReviewedCommit)
	if _, err := os.Stat(prepared.Delivery.Worktree); !os.IsNotExist(err) {
		t.Fatalf("restart precondition: delivery worktree already exists or cannot be checked: %v", err)
	}
	previousOwner := engine.Owner()
	engine = reopenPortableDeliveryEngine(t, ctx, engine)
	assertPortableReviewAccepted(t, ctx, engine, first)
	assertPortableReviewAccepted(t, ctx, engine, second)
	if engine.Owner() == previousOwner {
		t.Fatal("fixture did not replace the supervisor owner")
	}
	assertCompletedPortableDelivery(t, ctx, engine, second)
	paused, version := localStoredTask(t, engine, first.ID)
	if version != pausedVersion || paused.Stage != "paused" || paused.Delivery != nil || paused.ReviewedCommit != first.ReviewedCommit {
		t.Fatalf("peer finalization altered the paused record: version=%d plan=%+v", version, paused)
	}
	requireWorkflowSuccess(t, engine.SetPlanPaused(ctx, first.ID, version, false))
	firstDelivery := assertCompletedPortableDelivery(t, ctx, engine, first)
	secondDelivery, _ := localStoredTask(t, engine, second.ID)
	if firstDelivery.Branch == secondDelivery.Delivery.Branch || firstDelivery.Worktree == secondDelivery.Delivery.Worktree ||
		firstDelivery.Tree == secondDelivery.Delivery.Tree {
		t.Fatalf("crossed delivery identities: A=%+v B=%+v", firstDelivery, secondDelivery.Delivery)
	}
	for i, d := range []*model.LocalDelivery{firstDelivery, secondDelivery.Delivery} {
		content, err := os.ReadFile(filepath.Join(d.Worktree, concurrentDeliveryFixtureCandidateFile))
		requireWorkflowSuccess(t, err)
		if want := []string{"reviewed candidate\n", secondCandidateBody}[i]; string(content) != want {
			t.Fatalf("plan %d delivered peer content: got %q want %q", i+1, content, want)
		}
	}
	if got := submissionGit(t, project.Path, gitRevParseCommand, "refs/heads/main"); got != mainBefore {
		t.Fatalf("local delivery modified the source branch: %s != %s", got, mainBefore)
	}
	refs := strings.Fields(submissionGit(t, project.Path, "for-each-ref", "--format=%(refname)", "refs/heads/codex/delivery/"))
	if len(refs) != 2 {
		t.Fatalf("expected exactly two delivery branches after restart, got %v", refs)
	}
	assertOneDeliveryNoticeEach(t, ctx, engine, first.ID, second.ID)
}

func secondAcceptedDeliveryCandidate(t *testing.T, ctx context.Context, engine *Engine, template model.ExecutionPlan, project model.Project) model.ExecutionPlan {
	t.Helper()
	second := model.ExecutionPlan{
		ID: "second-concurrent-accepted-plan", ProjectID: project.ID, Mode: template.Mode,
		Stage: "planning", PlanVersion: 1, Issue: template.Issue, Selection: template.Selection, Plan: template.Plan,
	}
	requireWorkflowSuccess(t, engine.WriteLocalPlan(ctx, project, &second))
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "execution_plans", second.ID, second, 0))
	view, err := engine.ReadLocalPlan(ctx, second.ID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, engine.ReviewLocalPlan(ctx, second.ID, 1, "approve", view.Hash, ""))
	second, version := localStoredTask(t, engine, second.ID)

	second.Branch = "independent-accepted-candidate"
	second.Worktree = filepath.Join(engine.Root, "independent-worktree")
	second.Base = template.Base
	submissionGit(t, project.Path, "worktree", "add", "-b", second.Branch, second.Worktree, second.Base)
	requireWorkflowSuccess(t, os.WriteFile(filepath.Join(second.Worktree, concurrentDeliveryFixtureCandidateFile), []byte(secondCandidateBody), 0600))
	submissionGit(t, second.Worktree, "add", concurrentDeliveryFixtureCandidateFile)
	submissionGit(t, second.Worktree, "commit", "-m", "independent reviewed candidate")
	second.ReviewedCommit = submissionGit(t, second.Worktree, gitRevParseCommand, "HEAD")
	second.Stage, second.ChecksPassed = "submitting", true
	second.VerificationID, second.ReviewID = "verification-second-concurrent", second.ID+":1:1"
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "execution_plans", second.ID, second, version))
	provenance := acceptedReviewProvenance(second, project.PolicyHash)
	verification := map[string]any{"stage": "running", "provenance": provenance}
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "verification", second.VerificationID, verification, 0))
	fixture := acceptedReviewFixture{ctx: ctx, engine: engine, task: second, project: project}
	verification["stage"], verification["report"] = "complete", fixture.completeReport(t)
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "verification", second.VerificationID, verification, 1))
	review := model.Review{Provenance: &provenance, Requirements: []model.RequirementResult{{ID: "REQ-RESULT", Status: "pass",
		Code: []string{concurrentDeliveryFixtureCandidateFile}, Evidence: []string{"independent verified candidate"}, Explanation: "The second snapshot satisfies the requirement."}}}
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "reviews", second.ReviewID, review, 0))
	return second
}

func assertPortableReviewAccepted(t *testing.T, ctx context.Context, engine *Engine, plan model.ExecutionPlan) {
	t.Helper()
	_, evidence, accepted, err := engine.LoadAcceptedReviewReport(ctx, plan.ID)
	if err != nil || !accepted || evidence.Provenance.Head != plan.ReviewedCommit || evidence.Provenance.Task != plan.ID {
		t.Fatalf("plan %s has no matching accepted evidence: %v, %+v", plan.ID, err, evidence)
	}
}

func assertCrossPlanEvidenceRejected(t *testing.T, ctx context.Context, engine *Engine, first, peer model.ExecutionPlan, project model.Project) {
	t.Helper()
	foreign, version := localStoredTask(t, engine, first.ID)
	foreign.ReviewID = peer.ReviewID
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "execution_plans", first.ID, foreign, version))
	_, _, accepted, err := engine.LoadAcceptedReviewReport(ctx, first.ID)
	if accepted || err == nil || !strings.Contains(err.Error(), "review provenance does not match") {
		t.Fatalf("wrong peer review result: accepted=%v err=%v", accepted, err)
	}
	requireWorkflowSuccess(t, engine.Store.Put(ctx, "execution_plans", first.ID, first, version+1))

	// Isolate verification provenance; otherwise an unrelated ReviewID can
	// reject the record before this gate executes.
	foreign = first
	foreign.VerificationID = peer.VerificationID
	_, _, err = engine.loadVerificationEvidence(ctx, foreign, project)
	if err == nil || !strings.Contains(err.Error(), "verification evidence does not match") {
		t.Fatalf("wrong peer verification result: %v", err)
	}
	assertPortableReviewAccepted(t, ctx, engine, first)
}

func reopenPortableDeliveryEngine(t *testing.T, ctx context.Context, prior *Engine) *Engine {
	t.Helper()
	requireWorkflowSuccess(t, prior.Store.Close())
	reopened, err := store.Open(filepath.Join(prior.Root, "state.db"))
	requireWorkflowSuccess(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	next := New(reopened, prior.GitHub, prior.Root)
	next.PlansRoot, next.Adapter = prior.PlansRoot, prior.Adapter
	requireWorkflowSuccess(t, reopened.RecoverInterrupted(ctx, next.Owner()))
	return next
}

func assertCompletedPortableDelivery(t *testing.T, ctx context.Context, engine *Engine, want model.ExecutionPlan) *model.LocalDelivery {
	t.Helper()
	for range 3 {
		requireWorkflowSuccess(t, engine.Step(ctx, want.ID))
	}
	finished, version := localStoredTask(t, engine, want.ID)
	delivery := finished.Delivery
	if finished.Stage != "complete" || finished.PR != "" || delivery == nil || delivery.Stage != "complete" {
		t.Fatalf("not a completed local delivery: %s %+v", finished.Stage, delivery)
	}
	if delivery.ReviewID != want.ReviewID || delivery.VerificationID != want.VerificationID ||
		delivery.ReviewedCommit != want.ReviewedCommit || delivery.Base != want.Base {
		t.Fatalf("delivery %s has wrong provenance: %+v", want.ID, delivery)
	}
	if got := submissionGit(t, delivery.Worktree, gitRevParseCommand, "HEAD", "refs/heads/"+delivery.Branch); got != want.ReviewedCommit+"\n"+want.ReviewedCommit {
		t.Fatalf("delivery branch or worktree diverged: %q", got)
	}
	if got := submissionGit(t, delivery.Worktree, gitRevParseCommand, "HEAD^{tree}"); got != delivery.Tree {
		t.Fatalf("delivery tree diverged: %q != %q", got, delivery.Tree)
	}
	requireWorkflowSuccess(t, engine.Step(ctx, want.ID))
	if _, after := localStoredTask(t, engine, want.ID); after != version {
		t.Fatal("terminal plan replay rewrote completed delivery")
	}
	return delivery
}

func assertOneDeliveryNoticeEach(t *testing.T, ctx context.Context, engine *Engine, ids ...string) {
	t.Helper()
	inbox, err := engine.Store.List(ctx, "inbox")
	requireWorkflowSuccess(t, err)
	count := map[string]int{}
	for _, record := range inbox {
		notice, err := store.Decode[model.Notification](record)
		requireWorkflowSuccess(t, err)
		if notice.Kind == "delivery" {
			count[notice.TaskID]++
		}
	}
	if len(count) != len(ids) {
		t.Fatalf("unexpected delivery notices: %v", count)
	}
	for _, id := range ids {
		if count[id] != 1 {
			t.Fatalf("expected one durable delivery notice for %s, got %d", id, count[id])
		}
	}
}
