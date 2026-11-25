package workflow

import (
	"errors"
)

func (step *taskStep) coding() (bool, error) {
	var err error
	if step.task.Plan == nil {
		return true, errors.New("plan required")
	}
	if step.task.IdeaID != "" {
		err = step.codeWithPreparation()
	} else {
		err = step.code(step.ctx, step.project, &step.task)
	}
	if err != nil {
		return true, err
	}
	if step.task.Stage == "coding" {
		step.task.Stage = "review"
		step.task.ChecksPassed = true
	}
	if step.task.ChecksPassed {
		step.notify("review", "Implementation verified; Reviewer Agent starts next")
	}
	return false, nil
}
