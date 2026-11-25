package workflow

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// CancelPlanner commits revocation before stopping the worker. An already
// committed publication advances the idea version and makes cancellation stale.
func (e *Engine) CancelPlanner(ctx context.Context, ideaID string, expected int) error {
	record, err := e.Store.Get(ctx, "ideas", ideaID)
	if err != nil {
		return err
	}
	idea, err := store.Decode[model.Idea](record)
	if err != nil {
		return err
	}
	if record.Version != expected || (idea.DecisionStatus != "approved" && idea.DecisionStatus != "in_process") || idea.ArchivedAt != "" || idea.PlanID != "" {
		return store.ErrConflict
	}
	var attemptID string
	err = e.Store.DB.QueryRowContext(ctx, "SELECT planner_run_id FROM planner_runs WHERE idea_id=? AND status='in_process'", ideaID).Scan(&attemptID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if attemptID != "" && idea.DecisionStatus != "in_process" {
		return store.ErrConflict
	}

	planID := model.PlanID("idea:" + ideaID)
	changes, err := e.plannerCancellationChanges(ctx, idea, record.Version, planID, attemptID)
	if err != nil {
		return err
	}
	if err := e.Store.Write(ctx, changes, nil); err != nil {
		return err
	}
	store.AfterCommit(ctx, func() { e.Stop(planID) })
	return nil
}

func (e *Engine) plannerCancellationChanges(ctx context.Context, idea model.Idea, version int, planID, attemptID string) ([]store.Change, error) {
	planRecord, err := e.Store.Get(ctx, "execution_plans", planID)
	if errors.Is(err, store.ErrNotFound) && idea.DecisionStatus == "approved" && attemptID == "" {
		// Intake might be creating a plan from this same approval. The
		// transaction must either cancel that plan or prove it is still absent.
		return []store.Change{store.VersionPrecondition("execution_plans", planID, 0), cancelUnclaimedPlannerIdea(idea, version)}, nil
	}
	if err != nil {
		return nil, err
	}
	plan, err := store.Decode[model.ExecutionPlan](planRecord)
	if err != nil {
		return nil, err
	}
	if plan.IdeaID != idea.ID || plan.IdeationRunID != idea.RunID || plan.ProjectID != idea.ProjectID || !plannerCanBeCancelled(plan, idea.DecisionStatus, attemptID != "") {
		return nil, store.ErrConflict
	}
	var changes []store.Change
	if attemptID != "" {
		revocation, err := e.revokeActivePlannerSession(ctx, plan, attemptID)
		if err != nil {
			return nil, err
		}
		changes = append(changes, store.VersionPrecondition("ideas", idea.ID, version), revocation)
	} else {
		changes = append(changes, cancelUnclaimedPlannerIdea(idea, version))
	}
	plan.Stage, plan.ResumeStage, plan.Error = "cancelled", "", model.PlannerCancellationReason
	plan.Wait = nil
	plan.Handoff.NextAction = "Planner cancelled. Explicitly retry the idea to authorize another planning attempt."
	return append(changes, store.Change{Kind: "execution_plans", ID: planID, Value: plan, Expected: planRecord.Version}), nil
}

func plannerCanBeCancelled(plan model.ExecutionPlan, ideaStatus string, claimed bool) bool {
	if claimed {
		return plan.Stage == "planning"
	}
	if plan.Stage == "planning" || (plan.ResumeStage == "planning" && (plan.Stage == "waiting_quota" || plan.Stage == "paused")) {
		return true
	}
	// A new approval may be cancelled before intake resets its prior task.
	return ideaStatus == "approved" && (plan.Stage == "cancelled" || plan.Stage == "paused")
}

func cancelUnclaimedPlannerIdea(idea model.Idea, version int) store.Change {
	idea.DecisionStatus, idea.ErrorReason = "error", model.PlannerCancellationReason
	return store.Change{Kind: "ideas", ID: idea.ID, Value: idea, Expected: version}
}

func (e *Engine) revokeActivePlannerSession(ctx context.Context, plan model.ExecutionPlan, attemptID string) (store.Change, error) {
	record, err := e.Store.Get(ctx, "sessions", attemptID)
	if err != nil {
		return store.Change{}, err
	}
	attempt, err := store.Decode[model.Attempt](record)
	if err != nil {
		return store.Change{}, err
	}
	if attempt.AttemptID != attemptID || attempt.Task != plan.ID || attempt.IdeaID != plan.IdeaID || attempt.ProjectID != plan.ProjectID || attempt.PlanVersion != plan.PlanVersion {
		return store.Change{}, store.ErrConflict
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	success := false
	attempt.Finished, attempt.Status, attempt.Error, attempt.FinishedAt, attempt.Success = true, "cancelled", model.PlannerCancellationReason, now, &success
	attempt.Owner, attempt.LeaseExpiresAt, attempt.ProcessState = "", now, "cancellation_requested"
	attempt.ExecutionGeneration++
	return store.Change{Kind: "sessions", ID: attemptID, Value: attempt, Expected: record.Version}, nil
}

// WaitPlannerCleanup reports cleanup independently from the durable decision.
// Revocation remains committed even if a broken provider cannot be drained.
func (e *Engine) WaitPlannerCleanup(ctx context.Context, ideaID string) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		e.mu.Lock()
		_, running := e.running[model.PlanID("idea:"+ideaID)]
		e.mu.Unlock()
		if !running {
			return e.reconcileStandaloneRuntimes(ctx)
		}
		select {
		case <-ctx.Done():
			return errors.New("Planner cancellation committed; worker cleanup is not yet confirmed: " + ctx.Err().Error())
		case <-ticker.C:
		}
	}
}
