package workflow

import "errors"

func (step *taskStep) planReady() (bool, error) {
	if step.task.Plan == nil {
		return false, errors.New("ready plan missing")
	}
	if err := ValidateUnitScopes(step.project, step.task.Plan.Units); err != nil {
		return false, err
	}
	return false, step.WriteLocalPlan(step.ctx, step.project, &step.task)
}
