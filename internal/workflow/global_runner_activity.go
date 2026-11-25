package workflow

import (
	"context"
	"errors"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// SetRunnersStopped applies an inspected desired state, never a blind toggle.
// Settings and every affected durable wait/transition share one commit.
func (e *Engine) SetRunnersStopped(ctx context.Context, expected int, stopped bool) (map[string]any, error) {
	settingsRecord, err := e.Store.Get(ctx, "settings", "global")
	if err != nil {
		return nil, err
	}
	if settingsRecord.Version != expected {
		return nil, store.ErrConflict
	}
	settings, err := store.Decode[Settings](settingsRecord)
	if err != nil {
		return nil, err
	}
	settings.Stopped = stopped
	changes := []store.Change{{Kind: "settings", ID: "global", Value: settings, Expected: settingsRecord.Version}}
	plans, err := e.Store.List(ctx, "execution_plans")
	if err != nil {
		return nil, err
	}
	planChanges, changed, err := globalPlanActivityChanges(plans, stopped)
	if err != nil {
		return nil, err
	}
	changes = append(changes, planChanges...)
	operations, err := e.Store.List(ctx, "operations")
	if err != nil {
		return nil, err
	}
	operationChanges, err := globalOperationActivityChanges(operations, stopped)
	if err != nil {
		return nil, err
	}
	changes = append(changes, operationChanges...)
	if err := e.Store.Write(ctx, changes, nil); err != nil {
		return nil, err
	}
	if stopped {
		store.AfterCommit(ctx, e.StopAll)
	}
	label := "resumed"
	if stopped {
		label = "paused"
	}
	return map[string]any{"stopped": stopped, label: changed}, nil
}

func globalPlanActivityChanges(plans []model.Record, stopped bool) ([]store.Change, int, error) {
	var changes []store.Change
	changed := 0
	for _, record := range plans {
		plan, err := store.Decode[model.ExecutionPlan](record)
		if err != nil {
			return nil, 0, err
		}
		active, err := setGlobalPlanActivity(&plan, stopped)
		if err != nil {
			return nil, 0, err
		}
		if !active {
			continue
		}
		changes = append(changes, store.Change{Kind: "execution_plans", ID: plan.ID, Value: plan, Expected: record.Version})
		changed++
	}
	return changes, changed, nil
}

func setGlobalPlanActivity(plan *model.ExecutionPlan, stopped bool) (bool, error) {
	if stopped {
		switch plan.Stage {
		case "awaiting_selection", "paused", "complete", "pr_created", "cancelled":
			return false, nil
		}
		plan.ResumeStage, plan.Stage, plan.Error = plan.Stage, "paused", model.GlobalRunnerStopReason
	} else {
		if plan.Stage != "paused" || plan.Error != model.GlobalRunnerStopReason {
			return false, nil
		}
		if !plan.HasSelection() || plan.PR != "" {
			return false, errors.New("globally paused plan has no valid local execution authorization")
		}
		stage := plan.ResumeStage
		if stage == "" || stage == "paused" {
			stage = resumedPlanStage(*plan)
		}
		plan.Stage, plan.ResumeStage, plan.Error = stage, "", ""
		plan.Wait = nil
		plan.UnchangedFailures, plan.Recoveries = 0, 0
		plan.RecoveryGeneration++
	}
	return true, nil
}

func globalOperationActivityChanges(operations []model.Record, stopped bool) ([]store.Change, error) {
	var changes []store.Change
	for _, record := range operations {
		op, err := store.Decode[model.Operation](record)
		if err != nil {
			return nil, err
		}
		if !setGlobalIdeationActivity(&op, stopped) {
			continue
		}
		changes = append(changes, store.Change{Kind: "operations", ID: record.ID, Value: op, Expected: record.Version})
	}
	return changes, nil
}

func setGlobalIdeationActivity(op *model.Operation, stopped bool) bool {
	if op.Kind != "ideation" {
		return false
	}
	if stopped {
		if op.Stage != "running" {
			return false
		}
		op.Stage, op.Error = "paused", model.GlobalRunnerStopReason
	} else {
		if op.Stage != "paused" || op.Error != model.GlobalRunnerStopReason {
			return false
		}
		op.Stage, op.Error, op.FinishedAt = "queued", "", ""
	}
	return true
}
