package workflow

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestPlannerCancelBeforeIntakeFencesStaleApproval(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := t.Context()
	record, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	approved, err := store.Decode[model.Idea](record)
	requireWorkflowSuccess(t, err)
	project, err := e.Project(ctx, idea.ProjectID)
	requireWorkflowSuccess(t, err)
	if err := e.CancelPlanner(ctx, idea.ID, record.Version); err != nil {
		t.Fatalf("cancel before a planner has claimed the idea: %v", err)
	}
	// A concurrent intake can have read the approved idea before cancellation.
	requireWorkflowSuccess(t, e.intakeApprovedIdea(ctx, record, approved, project))
	assertCancelledPlannerIdeaWithoutAttempts(t, e, idea.ID)
	if _, err := e.Store.Get(ctx, "execution_plans", model.PlanID("idea:"+idea.ID)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stale approval still created a plan: %v", err)
	}
}

func TestPlannerCancelWhileDispatchCapacityIsUnavailable(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := t.Context()
	settings := e.settings(ctx)
	settings.GlobalSlots = 1
	settingsRecord, err := e.Store.Get(ctx, "settings", "global")
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.Store.Put(ctx, "settings", "global", settings, settingsRecord.Version))
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.mu.Lock()
	e.running["capacity-fixture"] = func() {}
	e.mu.Unlock()
	e.dispatch(ctx)
	e.background.Wait()
	planID := model.PlanID("idea:" + idea.ID)
	reserved, err := e.Store.Get(ctx, "execution_plans", planID)
	requireWorkflowSuccess(t, err)
	plan, err := store.Decode[model.ExecutionPlan](reserved)
	requireWorkflowSuccess(t, err)
	if plan.Stage != "planning" || launches.Load() != 0 {
		t.Fatalf("fixture did not remain queued for capacity: stage=%s launches=%d", plan.Stage, launches.Load())
	}
	record, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	if err := e.CancelPlanner(ctx, idea.ID, record.Version); err != nil {
		t.Fatalf("cannot cancel a queued plan while capacity is full: %v", err)
	}
	e.mu.Lock()
	delete(e.running, "capacity-fixture")
	e.mu.Unlock()
	e.dispatch(ctx)
	e.background.Wait()
	assertCancelledPlannerIdeaWithoutAttempts(t, e, idea.ID)
	savedRecord, err := e.Store.Get(ctx, "execution_plans", planID)
	requireWorkflowSuccess(t, err)
	saved, err := store.Decode[model.ExecutionPlan](savedRecord)
	requireWorkflowSuccess(t, err)
	if saved.Stage != "cancelled" || launches.Load() != 0 || savedRecord.Version != reserved.Version+1 {
		t.Fatalf("releasing capacity revived cancelled planning: stage=%s launches=%d version %d -> %d", saved.Stage, launches.Load(), reserved.Version, savedRecord.Version)
	}
}

func assertCancelledPlannerIdeaWithoutAttempts(t *testing.T, e *Engine, ideaID string) {
	t.Helper()
	record, err := e.Store.Get(t.Context(), "ideas", ideaID)
	requireWorkflowSuccess(t, err)
	idea, err := store.Decode[model.Idea](record)
	requireWorkflowSuccess(t, err)
	if idea.DecisionStatus != "error" || idea.ErrorReason != "user_cancelled" || idea.PlanID != "" {
		t.Fatalf("cancel did not durably revoke authoring: %+v", idea)
	}
	var runs int
	requireWorkflowSuccess(t, e.Store.DB.QueryRowContext(t.Context(), "SELECT count(*) FROM planner_runs WHERE idea_id=?", idea.ID).Scan(&runs))
	if runs != 0 {
		t.Fatalf("cancellation manufactured a provider attempt: %d", runs)
	}
}

func TestPlannerCancelConflictsWhenIntakeCreatesPlanBeforeCommit(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := t.Context()
	record, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	staged, batch := store.Stage(ctx)
	requireWorkflowSuccess(t, e.CancelPlanner(staged, idea.ID, record.Version))
	e.intakeApprovedIdeas(ctx)
	if err := batch.Commit(ctx, e.Store, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cancellation lost the newly created plan: %v", err)
	}
	requireWorkflowSuccess(t, e.CancelPlanner(ctx, idea.ID, record.Version))
	assertCancelledPlannerIdeaWithoutAttempts(t, e, idea.ID)
	plan, err := e.Store.Get(ctx, "execution_plans", model.PlanID("idea:"+idea.ID))
	requireWorkflowSuccess(t, err)
	saved, err := store.Decode[model.ExecutionPlan](plan)
	requireWorkflowSuccess(t, err)
	if saved.Stage != "cancelled" {
		t.Fatalf("pre-claim cancellation missed the pending plan: %s", saved.Stage)
	}
}

func TestPlannerCancelFencesMarkdownProducedByAnObsoleteWorker(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := t.Context()
	started, produce := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-produce:
		default:
			close(produce)
		}
		e.background.Wait()
	})
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(run context.Context, input provider.Input, hooks provider.Hooks) (provider.Output, error) {
			close(started)
			<-produce
			// Model a worker that didn't stop and returned valid assigned
			// output after revocation. It still lacks publication authority.
			return markdownPlannerAdapter(t, &launches).Run(context.WithoutCancel(run), input, hooks)
		})
	}
	e.dispatch(ctx)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("planner did not reach the fixture")
	}
	record, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	var output, attemptID string
	requireWorkflowSuccess(t, e.Store.DB.QueryRowContext(ctx, "SELECT markdown_path,planner_run_id FROM planner_runs WHERE idea_id=?", idea.ID).Scan(&output, &attemptID))
	requireWorkflowSuccess(t, e.CancelPlanner(ctx, idea.ID, record.Version))
	close(produce)
	e.background.Wait()
	if launches.Load() != 1 {
		t.Fatal("fixture did not deliver a late result")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("revoked session published a file: %s %v", output, err)
	}
	savedRecord, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	saved, err := store.Decode[model.Idea](savedRecord)
	requireWorkflowSuccess(t, err)
	if saved.DecisionStatus != "error" || saved.ErrorReason != model.PlannerCancellationReason || saved.PlanID != "" {
		t.Fatalf("late result changed durable cancellation: %+v", saved)
	}
	sessionRecord, err := e.Store.Get(ctx, "sessions", attemptID)
	requireWorkflowSuccess(t, err)
	attempt, err := store.Decode[model.Attempt](sessionRecord)
	requireWorkflowSuccess(t, err)
	if attempt.Status != "cancelled" || attempt.Success == nil || *attempt.Success {
		t.Fatalf("late result changed revoked session: %+v", attempt)
	}
	e.dispatch(ctx)
	e.background.Wait()
	if launches.Load() != 1 {
		t.Fatal("cancelled planner requeued without approval")
	}
}

func TestPlannerPublicationWinsAgainstAnAlreadyStagedCancellation(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := t.Context()
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.intakeApprovedIdeas(ctx)
	planID := model.PlanID("idea:" + idea.ID)
	step, err := e.prepareStep(ctx, planID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, step.generateIdeaPlan())
	inProcess, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	staged, cancellation := store.Stage(ctx)
	requireWorkflowSuccess(t, e.CancelPlanner(staged, idea.ID, inProcess.Version))
	// Restart after bytes reached disk but before either competing SQL commit.
	restarted := New(e.Store, e.GitHub, e.Root)
	restarted.PlansRoot = e.PlansRoot
	restarted.Adapter = e.Adapter
	requireWorkflowSuccess(t, restarted.Step(ctx, planID))
	if err := cancellation.Commit(ctx, e.Store, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale cancellation changed the committed publication: %v", err)
	}
	latest, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	completed, err := store.Decode[model.Idea](latest)
	requireWorkflowSuccess(t, err)
	document, err := restarted.ReadLocalPlan(ctx, planID)
	requireWorkflowSuccess(t, err)
	paths, err := os.ReadDir(e.PlansRoot)
	requireWorkflowSuccess(t, err)
	if completed.DecisionStatus != "complete" || completed.PlanID != planID || document.Path != step.task.PlanDocument.Path ||
		launches.Load() != 1 || len(paths) != 1 || document.Hash != step.task.PlanDocument.Hash {
		t.Fatalf("completion did not retain one original plan: idea=%+v paths=%v launches=%d", completed, paths, launches.Load())
	}
}

func TestPlannerCancelFencesAClaimFromPriorPlanVersion(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := t.Context()
	e.intakeApprovedIdeas(ctx)
	record, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	approved, err := store.Decode[model.Idea](record)
	requireWorkflowSuccess(t, err)
	planID := model.PlanID("idea:" + idea.ID)
	old, err := e.Store.Get(ctx, "execution_plans", planID)
	requireWorkflowSuccess(t, err)
	oldPlan, err := store.Decode[model.ExecutionPlan](old)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.CancelPlanner(ctx, idea.ID, record.Version))
	revoked, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	approved.ErrorReason = ""
	requireWorkflowSuccess(t, e.Store.Put(ctx, "ideas", idea.ID, approved, revoked.Version))
	e.intakeApprovedIdeas(ctx)
	stale := model.Attempt{AttemptID: "revoked-attempt", IdeaID: idea.ID, ProjectID: idea.ProjectID, OperationID: idea.RunID,
		Task: planID, Role: "plan", PlanVersion: oldPlan.PlanVersion, Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := e.Store.Put(ctx, "sessions", stale.AttemptID, stale, 0); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale worker claimed the fresh approval: %v", err)
	}
	if _, err := e.Store.Get(ctx, "sessions", stale.AttemptID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stale claim leaked an attempt: %v", err)
	}
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.dispatch(ctx)
	e.background.Wait()
	latest, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	complete, err := store.Decode[model.Idea](latest)
	requireWorkflowSuccess(t, err)
	if complete.DecisionStatus != "complete" || complete.PlanID != planID || launches.Load() != 1 {
		t.Fatalf("explicit retry did not produce exactly one plan on the fresh version: %+v launches=%d", complete, launches.Load())
	}
}

func TestPlannerCancellationCannotMissAConcurrentFallbackClaim(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := t.Context()
	e.intakeApprovedIdeas(ctx)
	planID := model.PlanID("idea:" + idea.ID)
	claim := model.Attempt{AttemptID: "first", IdeaID: idea.ID, ProjectID: idea.ProjectID, OperationID: idea.RunID,
		Task: planID, Role: "plan", PlanVersion: 1, Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	requireWorkflowSuccess(t, e.Store.Put(ctx, "sessions", claim.AttemptID, claim, 0))
	current, err := e.Store.Get(ctx, "sessions", claim.AttemptID)
	requireWorkflowSuccess(t, err)
	claim.Finished, claim.Status, claim.Error = true, "failed", "fixture provider unavailable"
	requireWorkflowSuccess(t, e.Store.Put(ctx, "sessions", claim.AttemptID, claim, current.Version))
	inProcess, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	staged, cancellation := store.Stage(ctx)
	requireWorkflowSuccess(t, e.CancelPlanner(staged, idea.ID, inProcess.Version))
	claim.AttemptID, claim.Finished, claim.Status, claim.Error = "fallback", false, "running", ""
	requireWorkflowSuccess(t, e.Store.Put(ctx, "sessions", claim.AttemptID, claim, 0))
	if err := cancellation.Commit(ctx, e.Store, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("old cancellation missed the fallback that had already claimed the idea: %v", err)
	}
	latest, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.CancelPlanner(ctx, idea.ID, latest.Version))
	var status string
	requireWorkflowSuccess(t, e.Store.DB.QueryRowContext(ctx, "SELECT status FROM planner_runs WHERE planner_run_id=?", claim.AttemptID).Scan(&status))
	if status != "error" {
		t.Fatalf("fallback was left active after cancellation: %s", status)
	}
}

func TestPlannerCancellationWinsAgainstAnAlreadyStagedPublication(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	ctx := t.Context()
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.intakeApprovedIdeas(ctx)
	planID := model.PlanID("idea:" + idea.ID)
	step, err := e.prepareStep(ctx, planID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, step.generateIdeaPlan())
	staged, publication := store.Stage(ctx)
	requireWorkflowSuccess(t, e.Store.Put(staged, "execution_plans", planID, step.task, step.record.Version))
	inProcess, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.CancelPlanner(ctx, idea.ID, inProcess.Version))
	if err := publication.Commit(ctx, e.Store, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale publication overwrote durable cancellation: %v", err)
	}
	record, err := e.Store.Get(ctx, "ideas", idea.ID)
	requireWorkflowSuccess(t, err)
	saved, err := store.Decode[model.Idea](record)
	requireWorkflowSuccess(t, err)
	if saved.DecisionStatus != "error" || saved.ErrorReason != model.PlannerCancellationReason || saved.PlanID != "" {
		t.Fatalf("stale publication changed the cancelled idea: %+v", saved)
	}
	if _, err := e.ReadLocalPlan(ctx, planID); err == nil {
		t.Fatal("discarded publication exposed provisional recovery bytes as a reviewable plan")
	}
	e.dispatch(ctx)
	e.background.Wait()
	paths, err := os.ReadDir(e.PlansRoot)
	requireWorkflowSuccess(t, err)
	if launches.Load() != 1 || len(paths) != 1 {
		t.Fatalf("cancelled publication spawned duplicate work or files: launches=%d files=%v", launches.Load(), paths)
	}
}
