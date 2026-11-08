package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// Critical-risk acceptance must wait for an exact human authorization before local finalization.
func TestCriticalChangeWaitsForHumanBeforeLocalFinalization(t *testing.T) {
	ctx := context.Background()
	e, task, _, _ := criticalApprovalFixture(t)
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	before := submissionGit(t, project.Path, "rev-parse", "HEAD")
	for n := 0; n < 6; n++ {
		if err := e.Step(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	done, _ := store.Decode[model.Task](r)
	if done.Stage != "approval" || done.PR != "" || done.Approved || done.Delivery != nil || done.Approval != nil {
		t.Fatalf("approval bypass: %+v", done)
	}
	if after := submissionGit(t, project.Path, "rev-parse", "HEAD"); after != before {
		t.Fatal("waiting for critical approval changed the accepted commit")
	}
	publications, err := e.Store.List(ctx, "publications")
	if err != nil || len(publications) != 0 {
		t.Fatalf("critical local approval created remote publication state: %d %v", len(publications), err)
	}
}

// TestProductiveReviewHasNoPassCap verifies a task can be accepted after more than one hundred review passes.
func TestProductiveReviewHasNoPassCap(t *testing.T) {
	fixture := newAgenticReviewFixture(t, false)
	ctx, e := fixture.ctx, fixture.e
	if err := e.Step(ctx, fixture.task.ID); err != nil {
		t.Fatal(err)
	}
	candidate, version := localStoredTask(t, e, fixture.task.ID)
	if candidate.Stage != "review" || !candidate.ChecksPassed || candidate.PlanDocument == nil || candidate.PlanDocument.ApprovedHash == "" {
		t.Fatalf("review did not receive an approved, verified candidate: %+v", candidate)
	}
	candidate.ReviewPasses = 100
	if err := e.Store.Put(ctx, "tasks", candidate.ID, candidate, version); err != nil {
		t.Fatal(err)
	}
	if err := e.Step(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	accepted, _ := localStoredTask(t, e, candidate.ID)
	if accepted.Stage != "submitting" || accepted.ReviewPasses != 101 || !accepted.ChecksPassed || accepted.Delivery != nil {
		t.Fatalf("productive 101st review did not route to local delivery: %+v", accepted)
	}
	_, adherence, ok, err := e.LoadAcceptedReviewReport(ctx, accepted.ID)
	if err != nil || !ok || adherence.Score != 100 || len(accepted.Handoff.CompletedCriteria) != 1 {
		t.Fatalf("frozen requirement evidence was not accepted after the 101st review: adherence=%+v task=%+v err=%v", adherence, accepted, err)
	}
	done := fixture.completeLocalDelivery()
	fixture.verifyTDD(done)
	fixture.verifySubmission(done)
	fixture.reconcile(done)
	if done.Delivery == nil || done.Delivery.Stage != "complete" || done.PR != "" {
		t.Fatalf("accepted candidate did not complete local delivery: %+v", done)
	}
}

// TestPRDescriptionListsEachUnitOnce verifies the local finalizer receives each approved implementation unit once.
func TestPRDescriptionListsEachUnitOnce(t *testing.T) {
	ctx := context.Background()
	var prompt string
	e, task, project, record := acceptedDeliveryFixtureWith(t, nil, nil, &prompt)
	done := completeAcceptedLocalDeliveryFixture(t, e, task, project, record)
	planStart := strings.Index(prompt, "\nPlan: ")
	if planStart < 0 {
		t.Fatalf("local finalizer prompt omitted approved plan: %s", prompt)
	}
	planContext := prompt[planStart+len("\nPlan: "):]
	if planEnd := strings.Index(planContext, "\nReview: "); planEnd >= 0 {
		planContext = planContext[:planEnd]
	}
	var approvedPlan model.Plan
	if err := json.Unmarshal([]byte(planContext), &approvedPlan); err != nil {
		t.Fatalf("decode approved plan in local finalizer context: %v", err)
	}
	unitMatches := 0
	for _, unit := range approvedPlan.Units {
		if unit.ID == "main" && unit.Objective == "Write failing test, implement result, run checks" {
			unitMatches++
		}
	}
	if unitMatches != 1 {
		t.Fatalf("local finalizer received the approved unit objective %d times in its structured inventory; want once: %+v", unitMatches, approvedPlan.Units)
	}
	if done.Delivery == nil || done.Delivery.Stage != "complete" || done.PR != "" {
		t.Fatalf("unit handoff bypassed local-only delivery: %+v", done)
	}
	publications, err := e.Store.List(ctx, "publications")
	if err != nil || len(publications) != 0 {
		t.Fatalf("unit handoff created remote publication state: %d %v", len(publications), err)
	}
}

// Repeated unchanged corrections keep accumulating telemetry without an automatic pause.
func TestRepeatedUnchangedFailuresRemainRunnableAfterThreeRecoveries(t *testing.T) {
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
	if task.Stage != "repair" || task.Recoveries != 3 || task.UnchangedFailures != 0 {
		t.Fatalf("unchanged correction telemetry paused or reset unexpectedly: %+v", task)
	}
}

type permissionAgent struct{}

// Run requests write permission using the canonical worktree path to exercise alias handling.
func (permissionAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	canonical, err := filepath.EvalSymlinks(in.CWD)
	if err != nil {
		return provider.Output{}, err
	}
	allowed, err := h.Permission(ctx, provider.Permission{Tool: "fs/write_text_file", Paths: []string{filepath.Join(canonical, resultFile)}})
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: repo, Prompt: "test", ReadOnly: false, SessionID: ""})
	if err != nil || out.Text != "allowed" {
		t.Fatalf("alias permission: %+v %v", out, err)
	}
}
