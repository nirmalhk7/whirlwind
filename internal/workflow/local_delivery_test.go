package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
)

func TestPromoteReviewedSnapshotKeepsExactCommitAndCreatesDetachedWorktree(t *testing.T) {
	repo := newSubmissionRepository(t)
	delivery := filepath.Join(filepath.Dir(repo.source), "delivery")
	branch := "codex/delivery/task-v1"

	commit, err := promoteReviewedSnapshot(context.Background(), repo.source, delivery, branch, repo.base, repo.reviewed)
	if err != nil {
		t.Fatal(err)
	}
	if commit != repo.reviewed {
		t.Fatalf("promoted commit %q, want exact reviewed commit %q", commit, repo.reviewed)
	}
	if got := submissionGit(t, repo.source, "rev-parse", "refs/heads/"+branch); got != repo.reviewed {
		t.Fatalf("delivery branch points at %s, want reviewed commit %s", got, repo.reviewed)
	}
	if got := submissionGit(t, delivery, "rev-parse", "HEAD"); got != repo.reviewed {
		t.Fatalf("delivery worktree HEAD=%s, want reviewed commit %s", got, repo.reviewed)
	}
	if _, err := github.Run(context.Background(), delivery, "git", "symbolic-ref", "-q", "HEAD"); err == nil {
		t.Fatal("delivery worktree is attached to a branch")
	}
	if got := submissionGit(t, delivery, "rev-parse", "HEAD^{tree}"); got != submissionGit(t, repo.source, "rev-parse", repo.reviewed+"^{tree}") {
		t.Fatal("delivery worktree tree differs from reviewed tree")
	}
}

func TestPromoteReviewedSnapshotRecoversAfterRefBeforeWorktree(t *testing.T) {
	repo := newSubmissionRepository(t)
	delivery := filepath.Join(filepath.Dir(repo.source), "delivery")
	branch := "codex/delivery/task-v1"
	submissionGit(t, repo.source, "update-ref", "refs/heads/"+branch, repo.reviewed)
	if _, err := os.Stat(delivery); !os.IsNotExist(err) {
		t.Fatalf("precondition failed: delivery worktree already exists: %v", err)
	}
	commit, err := promoteReviewedSnapshot(context.Background(), repo.source, delivery, branch, repo.base, repo.reviewed)
	if err != nil || commit != repo.reviewed {
		t.Fatalf("promotion did not recover after ref checkpoint: commit=%q err=%v", commit, err)
	}
	if got := submissionGit(t, delivery, "rev-parse", "HEAD"); got != repo.reviewed {
		t.Fatalf("recovered worktree points at %s, want %s", got, repo.reviewed)
	}
}

func TestPromoteReviewedSnapshotRejectsChangedCandidateAndExistingBranch(t *testing.T) {
	t.Run("changed candidate", func(t *testing.T) {
		repo := newSubmissionRepository(t)
		if err := os.WriteFile(filepath.Join(repo.source, "unreviewed.txt"), []byte("change\n"), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := promoteReviewedSnapshot(context.Background(), repo.source, filepath.Join(filepath.Dir(repo.source), "delivery"), "codex/delivery/task-v1", repo.base, repo.reviewed)
		if err == nil {
			t.Fatal("dirty candidate was promoted")
		}
	})
	t.Run("competing delivery branch", func(t *testing.T) {
		repo := newSubmissionRepository(t)
		branch := "codex/delivery/task-v1"
		submissionGit(t, repo.source, "branch", branch, repo.base)
		_, err := promoteReviewedSnapshot(context.Background(), repo.source, filepath.Join(filepath.Dir(repo.source), "delivery"), branch, repo.base, repo.reviewed)
		if err == nil {
			t.Fatal("existing branch was overwritten")
		}
		if got := submissionGit(t, repo.source, "rev-parse", "refs/heads/"+branch); got != repo.base {
			t.Fatalf("existing branch moved from %s to %s", repo.base, got)
		}
	})
}

func TestDeliverySnapshotRejectsForeignCloneAtSameReviewedCommit(t *testing.T) {
	repo := newSubmissionRepository(t)
	if _, err := github.Run(context.Background(), repo.source, "git", "clone", "--shared", repo.source, repo.target); err != nil {
		t.Fatal(err)
	}
	if _, err := github.Run(context.Background(), repo.target, "git", "checkout", "--detach", repo.reviewed); err != nil {
		t.Fatal(err)
	}
	if err := ensureDetachedDeliveryWorktree(context.Background(), repo.source, repo.target, repo.reviewed); err == nil {
		t.Fatal("separate clone at the same commit was accepted as the reviewed delivery worktree")
	}
}

func TestDeliveryBranchCASDoesNotOverwriteCompetingCreator(t *testing.T) {
	repo := newSubmissionRepository(t)
	ref := "refs/heads/codex/delivery/task-v1"
	competing := submissionGit(t, repo.source, "rev-parse", repo.base)
	submissionGit(t, repo.source, "update-ref", ref, competing)
	zero, err := emptyObjectID(context.Background(), repo.source)
	if err != nil {
		t.Fatal(err)
	}
	err = createDeliveryBranchRef(context.Background(), repo.source, ref, repo.reviewed, zero)
	if err == nil {
		t.Fatal("compare-and-swap branch creation overwrote a competing creator")
	}
	if got := submissionGit(t, repo.source, "rev-parse", ref); got != competing {
		t.Fatalf("competing branch moved from %s to %s", competing, got)
	}
}

func TestSubmittingRequiresAcceptedReportBeforeCreatingDelivery(t *testing.T) {
	ctx := context.Background()
	e, task := localPlanFixture(t)
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	base := submissionGit(t, project.Path, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(project.Path, "candidate.txt"), []byte("reviewed candidate\n"), 0600); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, project.Path, "add", "candidate.txt")
	submissionGit(t, project.Path, "commit", "-m", "candidate")
	task.Stage = "submitting"
	task.Worktree, task.Base = project.Path, base
	task.ReviewedCommit = submissionGit(t, project.Path, "rev-parse", "HEAD")
	task.Branch = "main"
	task.ChecksPassed = true
	record, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "tasks", task.ID, task, record.Version); err != nil {
		t.Fatal(err)
	}
	record.Version++
	step := &taskStep{Engine: e, ctx: ctx, project: project, task: task, record: record}
	finished, err := step.submitting()
	if err == nil || !finished || step.task.Delivery != nil {
		t.Fatalf("unreviewed candidate entered delivery: finished=%v delivery=%+v err=%v", finished, step.task.Delivery, err)
	}
}

func TestPublishingRoutesToLocalDeliveryWithoutRemotePublication(t *testing.T) {
	step := &taskStep{task: model.Task{Stage: "publishing"}}
	finished, err := step.publishing()
	if finished || err != nil || step.task.Stage != "submitting" {
		t.Fatalf("publishing did not route to local delivery: finished=%v stage=%q err=%v", finished, step.task.Stage, err)
	}
	if err := step.publishPullRequest("publication", model.Publication{}, 0); err == nil {
		t.Fatal("direct remote publication path was not denied")
	}
}

func TestAcceptedCandidateCompletesAsExactLocalDelivery(t *testing.T) {
	ctx := context.Background()
	e, task, project, record := acceptedDeliveryFixture(t)
	step := &taskStep{Engine: e, ctx: ctx, project: project, task: task, record: record}
	for range 3 {
		finished, err := step.submitting()
		if err != nil {
			t.Fatal(err)
		}
		if finished {
			break
		}
		if err := e.Store.Put(ctx, "tasks", task.ID, step.task, step.record.Version); err != nil {
			t.Fatal(err)
		}
		step.record.Version++
	}
	if step.task.Stage != "complete" || step.task.Delivery == nil || step.task.Delivery.Stage != "complete" {
		t.Fatalf("local delivery did not complete: stage=%s delivery=%+v", step.task.Stage, step.task.Delivery)
	}
	delivery := step.task.Delivery
	if delivery.ReviewedCommit != task.ReviewedCommit || delivery.Base != task.Base || delivery.ReviewID != task.ReviewID || delivery.VerificationID != task.VerificationID {
		t.Fatalf("delivery provenance changed: %+v", delivery)
	}
	if got := submissionGit(t, task.Worktree, "rev-parse", "refs/heads/"+delivery.Branch); got != task.ReviewedCommit {
		t.Fatalf("delivery branch points at %s, want reviewed commit %s", got, task.ReviewedCommit)
	}
	if got := submissionGit(t, delivery.Worktree, "rev-parse", "HEAD"); got != task.ReviewedCommit {
		t.Fatalf("finalizer worktree points at %s, want reviewed commit %s", got, task.ReviewedCommit)
	}
	if delivery.Message != "Exact candidate accepted for local delivery." || len(delivery.UnmetRequirements) != 0 || delivery.Adherence == nil || delivery.Adherence.Score <= 80 {
		t.Fatalf("delivery report incomplete: %+v", delivery)
	}
	publications, err := e.Store.List(ctx, "publications")
	if err != nil || len(publications) != 0 || step.task.PR != "" {
		t.Fatalf("local delivery attempted remote publication: task PR=%q records=%d err=%v", step.task.PR, len(publications), err)
	}
}

func TestAcceptedDeliveryShowsEveryUnmetRequirementToFinalizer(t *testing.T) {
	ctx := context.Background()
	var prompt string
	requirements := make([]model.Requirement, 6)
	judgments := make([]model.RequirementResult, 6)
	for index := range requirements {
		id := fmt.Sprintf("REQ-%d", index+1)
		requirements[index] = model.Requirement{ID: id, Outcome: "Requirement " + id, EvidenceMethod: "Inspect candidate evidence"}
		status, explanation := "pass", "Verified from candidate evidence."
		if index == 5 {
			status, explanation = "unverified", "No evidence was supplied for this requirement."
		}
		judgments[index] = model.RequirementResult{ID: id, Status: status, Code: []string{"candidate.txt"}, Evidence: []string{"candidate evidence"}, Explanation: explanation}
	}
	e, task, project, record := acceptedDeliveryFixtureWith(t, requirements, judgments, &prompt)
	step := &taskStep{Engine: e, ctx: ctx, project: project, task: task, record: record}
	for range 3 {
		finished, err := step.submitting()
		if err != nil {
			t.Fatal(err)
		}
		if finished {
			break
		}
		if err := e.Store.Put(ctx, "tasks", task.ID, step.task, step.record.Version); err != nil {
			t.Fatal(err)
		}
		step.record.Version++
	}
	delivery := step.task.Delivery
	if step.task.Stage != "complete" || delivery == nil || delivery.Adherence == nil {
		t.Fatalf("accepted local delivery did not complete: stage=%s delivery=%+v", step.task.Stage, delivery)
	}
	if delivery.Adherence.Total != 6 || delivery.Adherence.Passed != 5 || delivery.Adherence.Score < 83.3 || delivery.Adherence.Score > 83.4 {
		t.Fatalf("delivery changed the computed adherence report: %+v", delivery.Adherence)
	}
	if len(delivery.UnmetRequirements) != 1 || delivery.UnmetRequirements[0] != "REQ-6: No evidence was supplied for this requirement." {
		t.Fatalf("delivery hid or changed the unmet requirement: %+v", delivery.UnmetRequirements)
	}
	if !strings.Contains(prompt, delivery.UnmetRequirements[0]) || strings.Contains(prompt, "there should be none") {
		t.Fatalf("finalizer was not given the truthful unmet requirement: %q", prompt)
	}
}

func acceptedDeliveryFixture(t *testing.T) (*Engine, model.Task, model.Project, model.Record) {
	return acceptedDeliveryFixtureWith(t, nil, nil, nil)
}

func acceptedDeliveryFixtureWith(t *testing.T, requirements []model.Requirement, judgments []model.RequirementResult, prompt *string) (*Engine, model.Task, model.Project, model.Record) {
	t.Helper()
	openshell.Install(t)
	ctx := context.Background()
	e, task := localPlanFixture(t)
	if len(requirements) > 0 {
		task.Plan.Requirements = requirements
		// Edit the published, unapproved document as a human would. Clearing its
		// identity now correctly restores the durable original instead of replacing it.
		if err := os.WriteFile(task.PlanDocument.Path, []byte(RenderPlanDocument(*task.Plan)), 0600); err != nil {
			t.Fatal(err)
		}
		project, err := e.Project(ctx, task.ProjectID)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.WriteLocalPlan(ctx, project, &task); err != nil {
			t.Fatal(err)
		}
		record, err := e.Store.Get(ctx, "tasks", task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Store.Put(ctx, "tasks", task.ID, task, record.Version); err != nil {
			t.Fatal(err)
		}
	}
	view, err := e.ReadLocalPlan(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, taskVersion := localStoredTask(t, e, task.ID)
	if err := e.ReviewLocalPlan(ctx, task.ID, taskVersion, "approve", view.Hash, ""); err != nil {
		t.Fatal(err)
	}
	task, _ = localStoredTask(t, e, task.ID)
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	base := submissionGit(t, project.Path, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(project.Path, "candidate.txt"), []byte("reviewed candidate\n"), 0600); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, project.Path, "add", "candidate.txt")
	submissionGit(t, project.Path, "commit", "-m", "candidate")
	task.Stage, task.Worktree, task.Branch = "submitting", project.Path, "main"
	task.Base, task.ReviewedCommit = base, submissionGit(t, project.Path, "rev-parse", "HEAD")
	task.ReviewID, task.VerificationID, task.ChecksPassed = task.ID+":1:1", "verification-1", true
	taskRecord, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "tasks", task.ID, task, taskRecord.Version); err != nil {
		t.Fatal(err)
	}
	projectRecord, err := e.Store.Get(ctx, "providers", "fake")
	if err != nil {
		t.Fatal(err)
	}
	providerConfig, err := store.Decode[model.Provider](projectRecord)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(providerConfig.Capabilities, "finalizer") {
		providerConfig.Capabilities = append(providerConfig.Capabilities, "finalizer")
	}
	if err := e.Store.Put(ctx, "providers", providerConfig.ID, providerConfig, projectRecord.Version); err != nil {
		t.Fatal(err)
	}
	putFixtureAgent(t, e.Store, "finalizer", model.AgentConfig{Role: "finalizer", Chain: []model.AgentChoice{{Provider: "fake", Model: "fixture"}}})
	e.Adapter = func(model.Provider) provider.Adapter { return localDeliverySummaryAdapter{prompt: prompt} }
	provenance := model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Head: task.ReviewedCommit, Base: task.Base, PolicyHash: project.PolicyHash, VerificationID: task.VerificationID, ReviewID: task.ReviewID}
	verification := map[string]any{"stage": "running", "provenance": provenance}
	if err := e.Store.Put(ctx, "verification", task.VerificationID, verification, 0); err != nil {
		t.Fatal(err)
	}
	evidence := &acceptedReviewFixture{ctx: ctx, engine: e, task: task, project: project}
	report := evidence.completeReport(t)
	verification["stage"], verification["report"] = "complete", report
	if err := e.Store.Put(ctx, "verification", task.VerificationID, verification, 1); err != nil {
		t.Fatal(err)
	}
	if len(judgments) == 0 {
		judgments = []model.RequirementResult{{ID: "REQ-RESULT", Status: "pass", Code: []string{"candidate.txt"}, Evidence: []string{"verified candidate"}, Explanation: "Candidate satisfies the requirement."}}
	}
	review := model.Review{Requirements: judgments, Provenance: &provenance}
	if err := e.Store.Put(ctx, "reviews", task.ReviewID, review, 0); err != nil {
		t.Fatal(err)
	}
	updated, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return e, task, project, updated
}

type localDeliverySummaryAdapter struct{ prompt *string }

func (adapter localDeliverySummaryAdapter) Run(_ context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
	if input.Role != "finalizer" || input.Purpose != "local_delivery_summary" || !input.ReadOnly {
		return provider.Output{}, errors.New("finalizer request was not isolated and read-only")
	}
	if strings.TrimSpace(input.CWD) == "" {
		return provider.Output{}, errors.New("finalizer worktree missing")
	}
	if adapter.prompt != nil {
		*adapter.prompt = input.Prompt
	}
	return provider.Output{Text: "Exact candidate accepted for local delivery.", AttemptID: "finalizer-attempt"}, nil
}
