package workflow

import (
	"errors"
	"fmt"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

func (step *taskStep) repair() (bool, error) {
	var err error
	step.task.Approved = false
	step.task.Approval = nil
	prompt, err := agents.Render("repair", map[string]string{"Plan": jsonText(step.task.Plan), "Findings": step.latestReview(step.ctx, fmt.Sprintf("%s:%d", step.task.ID, step.task.PlanVersion)), "Handoff": jsonText(step.task.Handoff)})
	if err != nil {
		return true, err
	}
	out, runErr := step.agent(step.ctx, step.project, step.task, provider.Input{Role: "code", CWD: step.task.Worktree, Prompt: prompt, ReadOnly: false, SessionID: ""})
	if runErr != nil {
		return true, runErr
	}
	step.task.LastAttemptID = out.AttemptID
	step.task.ImplementationAttempts = append(step.task.ImplementationAttempts, out.AttemptID)
	if err = step.commit(step.ctx, step.project, step.task.Worktree, "Address review findings"); err != nil {
		return true, err
	}
	if err = step.verifyRepair(); err != nil {
		return true, err
	}
	return false, nil
}

func (step *taskStep) verifyRepair() error {
	if err := step.verify(step.ctx, step.project, &step.task); err != nil {
		if errors.Is(err, store.ErrPersistence) {
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
