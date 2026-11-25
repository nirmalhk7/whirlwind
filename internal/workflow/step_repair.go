package workflow

import (
	"errors"
	"fmt"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
	"whirlwind/prompts/agents"
)

func (step *taskStep) repair() (bool, error) {
	project, err := repairProject(step.project, step.task.Plan)
	if err != nil {
		return true, err
	}
	step.task.Submission = nil
	step.task.Approved = false
	step.task.Approval = nil
	prompt, err := agents.Render("repair", map[string]string{"Plan": jsonText(step.task.Plan), "Findings": step.latestReview(step.ctx, fmt.Sprintf("%s:%d", step.task.ID, step.task.PlanVersion)), "Handoff": jsonText(step.task.Handoff)})
	if err != nil {
		return true, err
	}
	out, runErr := step.agent(step.ctx, project, step.task, provider.Input{Role: "code", CWD: step.task.Worktree, Prompt: prompt})
	if runErr != nil {
		return true, runErr
	}
	step.task.LastAttemptID = out.AttemptID
	step.task.ImplementationAttempts = append(step.task.ImplementationAttempts, out.AttemptID)
	if err = step.commit(step.ctx, project, step.task.Worktree, "Address review findings"); err != nil {
		return true, err
	}
	if err = step.verifyRepair(); err != nil {
		return true, err
	}
	return false, nil
}

func repairProject(project model.Project, plan *model.PlanSpecification) (model.Project, error) {
	if plan == nil {
		return project, errors.New("repair requires an approved plan")
	}
	if err := ValidateUnitScopes(project, plan.Units); err != nil {
		return project, err
	}
	project.Policy.AllowedPaths = nil
	for _, unit := range plan.Units {
		project.Policy.AllowedPaths = append(project.Policy.AllowedPaths, unit.AllowedPaths...)
	}
	return project, nil
}

func (step *taskStep) verifyRepair() error {
	if err := step.verify(step.ctx, step.project, &step.task); err != nil {
		if errors.Is(err, store.ErrPersistence) || errors.Is(err, verification.ErrJobSetup) {
			return err
		}
		step.task.ChecksPassed = false
		step.task.Stage = "repair"
		step.task.Error = err.Error()
		if x := step.progress(step.ctx, &step.task); x != nil {
			return x
		}
	} else {
		step.task.ChecksPassed = true
		step.task.Stage = "review"
		step.task.Error = ""
	}
	return nil
}
