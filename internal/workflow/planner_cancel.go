package workflow

import (
	"context"
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
	if record.Version != expected || idea.DecisionStatus != "in_process" || idea.ArchivedAt != "" || idea.PlanID != "" {
		return store.ErrConflict
	}
	var attemptID string
	if err := e.Store.DB.QueryRowContext(ctx, "SELECT planner_run_id FROM planner_runs WHERE idea_id=? AND status='in_process'", ideaID).Scan(&attemptID); err != nil {
		return err
	}
	sessionRecord, err := e.Store.Get(ctx, "sessions", attemptID)
	if err != nil {
		return err
	}
	attempt, err := store.Decode[model.Attempt](sessionRecord)
	if err != nil {
		return err
	}
	planRecord, err := e.Store.Get(ctx, "execution_plans", attempt.Task)
	if err != nil {
		return err
	}
	plan, err := store.Decode[model.ExecutionPlan](planRecord)
	if err != nil {
		return err
	}
	if plan.IdeaID != ideaID || plan.IdeationRunID != idea.RunID || attempt.ProjectID != idea.ProjectID || plan.Stage != "planning" {
		return store.ErrConflict
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	attempt.Finished, attempt.Status, attempt.Error, attempt.FinishedAt = true, "cancelled", "user_cancelled", now
	attempt.Owner, attempt.LeaseExpiresAt, attempt.ProcessState = "", now, "cancellation_requested"
	attempt.ExecutionGeneration++
	plan.Stage, plan.ResumeStage, plan.Error = "cancelled", "", "user_cancelled"
	plan.Handoff.NextAction = "Planner cancelled. Explicitly retry the idea to authorize another planning attempt."
	if err := e.Store.Write(ctx, []store.Change{{Kind: "sessions", ID: attemptID, Value: attempt, Expected: sessionRecord.Version}, {Kind: "execution_plans", ID: plan.ID, Value: plan, Expected: planRecord.Version}}, nil); err != nil {
		return err
	}
	store.AfterCommit(ctx, func() { e.Stop(plan.ID) })
	return nil
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
