package store

import (
	"context"
	"database/sql"
	"errors"

	"whirlwind/internal/model"
)

// A legacy successful provider exit still needs to be reconciled if its
// Planner run is in process. New exits have an explicitly nonterminal result.
func plannerPublicationCandidate(attempt model.Attempt) bool {
	return attempt.Finished && (attempt.Status == model.AttemptAwaitingPlanPublication && attempt.Success == nil ||
		attempt.Status == "complete" && attempt.Success != nil && *attempt.Success)
}

func failUnpublishedPlannerSessionTx(ctx context.Context, tx *sql.Tx, plan model.ExecutionPlan, now string) error {
	var id string
	err := tx.QueryRowContext(ctx, "SELECT planner_run_id FROM planner_runs WHERE idea_id=? AND status='in_process'", plan.IdeaID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	attempt, version, err := txResource[model.Attempt](ctx, tx, "sessions", id)
	if err != nil {
		return err
	}
	if attempt.Task != plan.ID || attempt.PlanVersion != plan.PlanVersion || !plannerPublicationCandidate(attempt) {
		return nil
	}
	success := false
	attempt.Status, attempt.Success, attempt.Error, attempt.FinishedAt = "failed", &success, plan.Error, now
	return put(ctx, tx, "sessions", id, attempt, version)
}

func finalizePlannerMetricTx(ctx context.Context, tx *sql.Tx, id string, success bool) error {
	metric, version, err := txResource[map[string]any](ctx, tx, "metrics", id)
	if errors.Is(err, ErrNotFound) {
		return nil // Legacy or manually seeded Planner attempts may lack metrics.
	}
	if err != nil {
		return err
	}
	if metric == nil {
		metric = map[string]any{}
	}
	metric["success"] = success
	delete(metric, "awaiting_plan_publication")
	return put(ctx, tx, "metrics", id, metric, version)
}
