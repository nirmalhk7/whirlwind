package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"whirlwind/internal/model"
)

// A Planner row shares the ACP attempt identity. The session and approved-idea
// claim commit together, before any child process is allowed to launch.
func syncPlannerAttemptTx(ctx context.Context, tx *sql.Tx, id string, before, data []byte) error {
	var attempt model.Attempt
	if err := json.Unmarshal(data, &attempt); err != nil {
		return err
	}
	previous, err := previousPlannerAttempt(before, attempt)
	if err != nil {
		return err
	}
	if attempt.IdeaID == "" {
		return nil
	}
	if attempt.Role != "plan" || attempt.AttemptID != id {
		return ErrConflict
	}
	idea, version, err := plannerAttemptIdeaTx(ctx, tx, attempt)
	if err != nil {
		return err
	}
	if len(before) == 0 {
		if err := createPlannerRunTx(ctx, tx, id, attempt, idea, version); err != nil {
			return err
		}
	} else if err := validateExistingPlannerRunTx(ctx, tx, id, idea.ID); err != nil {
		return err
	}
	if attempt.Finished && attempt.Status != "complete" && attempt.Status != model.AttemptAwaitingPlanPublication {
		if plannerPublicationCandidate(previous) {
			if err := finalizePlannerMetricTx(ctx, tx, id, false); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, "UPDATE planner_runs SET status='error',error_reason=?,finished_at=? WHERE planner_run_id=? AND status='in_process'", attempt.Error, attempt.FinishedAt, id)
		if err != nil {
			return err
		}
		return requireOneRow(result)
	}
	return nil
}

// PlannerRunLocation pins the publication destination before writing the file.
// A retry must reconcile this same destination instead of allocating another.
func (s *Store) PlannerRunLocation(ctx context.Context, attemptID, path string) error {
	if path == "" {
		return ErrConflict
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "UPDATE planner_runs SET markdown_path=? WHERE planner_run_id=? AND status='in_process' AND (markdown_path IS NULL OR markdown_path=?)", path, attemptID, path)
		if err != nil {
			return err
		}
		return requireOneRow(result)
	})
}

func plannerIdentityTx(ctx context.Context, tx *sql.Tx, attemptID string) (model.Idea, int, error) {
	var id, status string
	err := tx.QueryRowContext(ctx, "SELECT idea_id,status FROM planner_runs WHERE planner_run_id=?", attemptID).Scan(&id, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Idea{}, 0, ErrNotFound
	}
	if err != nil {
		return model.Idea{}, 0, err
	}
	idea, version, err := txResource[model.Idea](ctx, tx, "ideas", id)
	if err != nil {
		return idea, version, err
	}
	if status != "in_process" || idea.DecisionStatus != "in_process" || idea.ArchivedAt != "" || idea.PlanID != "" {
		return idea, version, ErrConflict
	}
	return idea, version, nil
}

func syncPlannerPublicationTx(ctx context.Context, tx *sql.Tx, before, data []byte) error {
	var plan, old model.ExecutionPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return err
	}
	if len(before) > 0 {
		if err := json.Unmarshal(before, &old); err != nil {
			return err
		}
		if old.IdeaID != plan.IdeaID || old.IdeationRunID != plan.IdeationRunID {
			return ErrConflict
		}
	}
	return applyPlannerPublicationTx(ctx, tx, old, plan)
}

func failPlannerIdeaTx(ctx context.Context, tx *sql.Tx, plan model.ExecutionPlan) error {
	idea, version, err := txResource[model.Idea](ctx, tx, "ideas", plan.IdeaID)
	if err != nil {
		return err
	}
	if idea.ProjectID != plan.ProjectID || idea.RunID != plan.IdeationRunID {
		return ErrConflict
	}
	if idea.DecisionStatus != "in_process" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := failUnpublishedPlannerSessionTx(ctx, tx, plan, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE planner_runs SET status='error',error_reason=?,finished_at=? WHERE idea_id=? AND status='in_process'", plan.Error, now, idea.ID); err != nil {
		return err
	}
	idea.DecisionStatus, idea.ErrorReason = "error", plan.Error
	return put(ctx, tx, "ideas", idea.ID, idea, version)
}

func previousPlannerAttempt(before []byte, attempt model.Attempt) (model.Attempt, error) {
	var old model.Attempt
	if len(before) > 0 {
		if err := json.Unmarshal(before, &old); err != nil {
			return old, err
		}
		if old.IdeaID != attempt.IdeaID {
			return old, ErrConflict
		}
	}
	return old, nil
}

func plannerAttemptIdeaTx(ctx context.Context, tx *sql.Tx, attempt model.Attempt) (model.Idea, int, error) {
	idea, version, err := txResource[model.Idea](ctx, tx, "ideas", attempt.IdeaID)
	if err != nil {
		return model.Idea{}, 0, err
	}
	if idea.ProjectID != attempt.ProjectID || idea.RunID != attempt.OperationID || idea.ArchivedAt != "" || idea.PlanID != "" || (idea.DecisionStatus != "approved" && idea.DecisionStatus != "in_process") {
		return model.Idea{}, 0, ErrConflict
	}
	plan, _, err := txResource[model.ExecutionPlan](ctx, tx, "execution_plans", attempt.Task)
	if err != nil {
		return model.Idea{}, 0, err
	}
	if plan.IdeaID != idea.ID || plan.IdeationRunID != idea.RunID || plan.ProjectID != idea.ProjectID {
		return model.Idea{}, 0, ErrConflict
	}
	return idea, version, nil
}

func createPlannerRunTx(ctx context.Context, tx *sql.Tx, id string, attempt model.Attempt, idea model.Idea, version int) error {
	var active int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM planner_runs WHERE idea_id=? AND status='in_process'", idea.ID).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return ErrConflict
	}
	if idea.DecisionStatus == "approved" {
		idea.DecisionStatus, idea.ErrorReason = "in_process", ""
		if err := put(ctx, tx, "ideas", idea.ID, idea, version); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO planner_runs(planner_run_id,idea_id,idea_run_id,timestamp,status) VALUES(?,?,?,?,'in_process')", id, idea.ID, idea.RunID, attempt.StartedAt)
	if err != nil {
		return err
	}
	return nil
}

func validateExistingPlannerRunTx(ctx context.Context, tx *sql.Tx, id, ideaID string) error {
	var savedIdea, status string
	if err := tx.QueryRowContext(ctx, "SELECT idea_id,status FROM planner_runs WHERE planner_run_id=?", id).Scan(&savedIdea, &status); err != nil {
		return err
	}
	if savedIdea != ideaID || status != "in_process" {
		return ErrConflict
	}
	return nil
}

func applyPlannerPublicationTx(ctx context.Context, tx *sql.Tx, old, plan model.ExecutionPlan) error {
	if plan.IdeaID != "" && old.Stage == "planning" && (plan.Stage == "paused" || plan.Stage == "cancelled") && plan.Error != "" && plan.Error != model.GlobalRunnerStopReason {
		return failPlannerIdeaTx(ctx, tx, plan)
	}
	completed, err := plannerPublicationAlreadyCompleteTx(ctx, tx, plan)
	if err != nil {
		return err
	}
	if completed {
		return nil
	}
	if plan.IdeaID == "" || plan.Stage != "plan_approval" || old.Stage == "plan_approval" {
		return nil
	}
	return completePlannerPublicationTx(ctx, tx, plan)
}

func completePlannerPublicationTx(ctx context.Context, tx *sql.Tx, plan model.ExecutionPlan) error {
	idea, version, err := plannerIdentityTx(ctx, tx, plan.LastAttemptID)
	if err != nil {
		return err
	}
	if idea.ID != plan.IdeaID || idea.ProjectID != plan.ProjectID || idea.RunID != plan.IdeationRunID || plan.PlanDocument == nil || plan.PlanDocument.Path == "" || plan.PlanDocument.Hash == "" || plan.Plan == nil || len(plan.Plan.Requirements) == 0 {
		return ErrConflict
	}
	attempt, attemptVersion, err := txResource[model.Attempt](ctx, tx, "sessions", plan.LastAttemptID)
	if err != nil {
		return err
	}
	if attempt.Task != plan.ID || attempt.PlanVersion != plan.PlanVersion || !plannerPublicationCandidate(attempt) {
		return ErrConflict
	}
	if attempt.Status == model.AttemptAwaitingPlanPublication {
		success := true
		attempt.Status, attempt.Success = "complete", &success
		if err := put(ctx, tx, "sessions", attempt.AttemptID, attempt, attemptVersion); err != nil {
			return err
		}
		if err := finalizePlannerMetricTx(ctx, tx, attempt.AttemptID, true); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, "UPDATE planner_runs SET status='completed',plan_id=?,finished_at=? WHERE planner_run_id=? AND status='in_process' AND markdown_path=?", plan.ID, attempt.FinishedAt, plan.LastAttemptID, plan.PlanDocument.Path)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		return err
	}
	idea.DecisionStatus, idea.PlanID, idea.ErrorReason = "complete", plan.ID, ""
	return put(ctx, tx, "ideas", idea.ID, idea, version)
}

func plannerPublicationAlreadyCompleteTx(ctx context.Context, tx *sql.Tx, plan model.ExecutionPlan) (bool, error) {
	if plan.IdeaID != "" {
		idea, _, err := txResource[model.Idea](ctx, tx, "ideas", plan.IdeaID)
		if err != nil {
			return false, err
		}
		if idea.DecisionStatus == "complete" {
			if idea.PlanID != plan.ID {
				return false, ErrConflict
			}
			return true, nil
		}
	}
	return false, nil
}
