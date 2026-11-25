package workflow

import (
	"context"
	"encoding/json"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) pendingDecisionOwner(ctx context.Context, taskID, attemptID string, planVersion int) (model.Record, model.ExecutionPlan, model.Attempt, string, error) {
	taskRecord, err := e.Store.Get(ctx, "execution_plans", taskID)
	if err != nil {
		return model.Record{}, model.ExecutionPlan{}, model.Attempt{}, "", err
	}
	task, err := store.Decode[model.ExecutionPlan](taskRecord)
	if err != nil {
		return taskRecord, task, model.Attempt{}, "", err
	}
	if task.Stage != "paused" || task.Wait == nil || task.Wait.Kind != "human_decision" || task.PlanVersion != planVersion || task.Wait.PlanVersion != planVersion || task.ResumeStage == "" || task.Wait.ResumeStage != task.ResumeStage {
		return taskRecord, task, model.Attempt{}, "", store.ErrConflict
	}
	if err := e.validatePendingDecisionAttempt(ctx, task, planVersion, attemptID); err != nil {
		return taskRecord, task, model.Attempt{}, "", err
	}
	attemptRecord, err := e.Store.Get(ctx, "sessions", attemptID)
	if err != nil {
		return taskRecord, task, model.Attempt{}, "", err
	}
	attempt, err := store.Decode[model.Attempt](attemptRecord)
	if err != nil {
		return taskRecord, task, attempt, "", err
	}
	if attempt.AttemptID != attemptID || attempt.Task != task.ID || attempt.PlanVersion != planVersion || !attempt.Finished {
		return taskRecord, task, attempt, "", store.ErrConflict
	}
	if !roleMayResumeStage(attempt.Role, task.ResumeStage) {
		return taskRecord, task, attempt, "", store.ErrConflict
	}
	return taskRecord, task, attempt, task.ResumeStage, nil
}

func uniquePendingDecisionAttempt(ctx context.Context, database *store.Store, taskID string, planVersion int, attemptID string) (bool, error) {
	attempts := map[string]bool{}
	for _, kind := range []string{"questions", "permissions"} {
		records, err := database.List(ctx, kind)
		if err != nil {
			return false, err
		}
		for _, record := range records {
			var owner struct {
				ID          string `json:"id"`
				TaskID      string `json:"plan_id"`
				AttemptID   string `json:"attempt_id"`
				PlanVersion int    `json:"plan_version"`
				Status      string `json:"status"`
				Decision    string `json:"decision"`
			}
			if err := json.Unmarshal(record.Data, &owner); err != nil {
				return false, err
			}
			ownedByTask := owner.TaskID == taskID
			isPending := owner.Status == "pending" || owner.Decision == "pending"
			if ownedByTask && isPending && owner.PlanVersion == planVersion && owner.AttemptID != "" {
				attempts[owner.AttemptID] = true
			}
		}
	}
	return len(attempts) == 1 && attempts[attemptID], nil
}

func latestPendingDecisionAttempt(ctx context.Context, database *store.Store, taskID string, planVersion int) (string, error) {
	pending, err := pendingDecisionOwners(ctx, database, taskID, planVersion)
	if err != nil {
		return "", err
	}
	latestByScope, err := latestDecisionAttemptsByScope(ctx, database, taskID, planVersion)
	if err != nil {
		return "", err
	}
	// A newer Reviewer preparation or parallel unit cannot supersede a Coding question.
	var latest scopedAttempt
	for _, candidate := range latestByScope {
		attempt := candidate.attempt
		if candidate.order > latest.order && attempt.Finished && attempt.Status == "paused" && pending[attempt.AttemptID] {
			latest = candidate
		}
	}
	if latest.attempt.AttemptID == "" {
		return "", store.ErrConflict
	}
	return latest.attempt.AttemptID, nil
}

func pendingDecisionOwners(ctx context.Context, database *store.Store, taskID string, planVersion int) (map[string]bool, error) {
	pending := map[string]bool{}
	for _, kind := range []string{"questions", "permissions"} {
		records, err := database.List(ctx, kind)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			var owner struct {
				TaskID      string `json:"plan_id"`
				AttemptID   string `json:"attempt_id"`
				PlanVersion int    `json:"plan_version"`
				Status      string `json:"status"`
				Decision    string `json:"decision"`
			}
			if err := json.Unmarshal(record.Data, &owner); err != nil {
				return nil, err
			}
			ownedByTask := owner.TaskID == taskID
			isPending := owner.Status == "pending" || owner.Decision == "pending"
			if ownedByTask && isPending && owner.PlanVersion == planVersion && owner.AttemptID != "" {
				pending[owner.AttemptID] = true
			}
		}
	}
	return pending, nil
}

type attemptScope struct{ role, purpose, unit string }
type scopedAttempt struct {
	attempt model.Attempt
	order   int64
}

func latestDecisionAttemptsByScope(ctx context.Context, database *store.Store, taskID string, planVersion int) (map[attemptScope]scopedAttempt, error) {
	records, err := database.List(ctx, "sessions")
	if err != nil {
		return nil, err
	}
	latestByScope := map[attemptScope]scopedAttempt{}
	for _, record := range records {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil {
			return nil, err
		}
		if attempt.Task == taskID && attempt.PlanVersion == planVersion {
			scope := attemptScope{attempt.Role, attempt.Purpose, attempt.UnitID}
			if record.CreatedOrder > latestByScope[scope].order {
				latestByScope[scope] = scopedAttempt{attempt, record.CreatedOrder}
			}
		}
	}
	return latestByScope, nil
}
