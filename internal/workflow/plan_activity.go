package workflow

import (
	"context"
	"errors"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) SetPlanPaused(ctx context.Context, id string, expected int, paused bool) error {
	plan, record, err := loadStepInput(ctx, e.Store, id)
	if err != nil {
		return err
	}
	if record.Version != expected || plan.Cancelled() {
		return store.ErrConflict
	}
	if err := setPlanPauseState(&plan, paused); err != nil {
		return err
	}
	if err := e.Store.Put(ctx, "execution_plans", id, plan, record.Version); err != nil {
		return err
	}
	if paused {
		store.AfterCommit(ctx, func() { e.Stop(id) })
	}
	return nil
}

func resumedPlanStage(plan model.ExecutionPlan) string {
	if plan.Plan == nil {
		return "planning"
	}
	if plan.Worktree == "" {
		return "coding"
	}
	for _, unit := range plan.Plan.Units {
		if unit.Stage != "integrated" {
			return "coding"
		}
	}
	return "repair"
}

func setPlanPauseState(plan *model.ExecutionPlan, paused bool) error {
	if paused {
		return pausePlanState(plan)
	}
	return resumePlanState(plan)
}

func pausePlanState(plan *model.ExecutionPlan) error {
	if plan.Stage == "complete" || plan.Stage == "pr_created" {
		return errors.New("completed plan has no active execution to pause")
	}
	if plan.IdeaID != "" && plan.Stage == "planning" {
		return errors.New("active Planner uses planner.cancel; execution pause starts after authoring")
	}
	if plan.Stage != "paused" {
		plan.ResumeStage = plan.Stage
	}
	plan.Stage, plan.Error = "paused", "Paused by user"
	return nil
}

func resumePlanState(plan *model.ExecutionPlan) error {
	if plan.Stage != "paused" || !plan.HasSelection() || plan.PR != "" {
		return store.ErrConflict
	}
	if plan.IdeaID != "" && plan.ResumeStage == "planning" {
		return errors.New("failed Planner requires explicit idea.retry_planning")
	}
	plan.Stage = resumedPlanStage(*plan)
	if plan.ResumeStage != "" && plan.ResumeStage != "paused" {
		plan.Stage = plan.ResumeStage
	} else if plan.Plan != nil {
		for _, question := range plan.Plan.Questions {
			if question.Answer == "" {
				plan.Stage = "questions"
			}
		}
	}
	plan.Error, plan.ResumeStage = "", ""
	plan.EligibilityOverride = !plan.Issue.Eligible()
	plan.UnchangedFailures, plan.Recoveries = 0, 0
	plan.Wait = nil
	plan.RecoveryGeneration++
	return nil
}
