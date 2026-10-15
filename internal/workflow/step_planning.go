package workflow

import (
	"errors"
	"fmt"
	"path/filepath"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

func (step *taskStep) planning() (bool, error) {
	path, err := step.planningWorktree()
	if err != nil {
		return true, err
	}
	prompt, err := step.planningPrompt()
	if err != nil {
		return true, err
	}
	out, err := step.agent(step.ctx, step.project, step.task, provider.Input{Role: "plan", CWD: path, Prompt: prompt, ReadOnly: true, SessionID: ""})
	if err != nil {
		return true, err
	}
	step.task.LastAttemptID = out.AttemptID
	plan, err := step.applyPlannerOutput(out.Text)
	if err != nil {
		return true, err
	}
	step.task.Handoff.NextAction = "implement plan"
	key := fmt.Sprintf("%s:%d", step.task.ID, step.task.PlanVersion)
	previous, _ := step.Store.Get(step.ctx, "plans", key)
	step.pending = append(step.pending, store.Change{Kind: "plans", ID: key, Value: plan, Expected: previous.Version})
	return false, nil
}
func (step *taskStep) applyPlannerOutput(text string) (model.Plan, error) {
	plan, err := decodePlan(text)
	if err != nil {
		return plan, err
	}
	return plan, step.applyPlan(plan)
}

func (step *taskStep) planningWorktree() (string, error) {
	path := filepath.Join(step.Root, "planning", step.task.ID, fmt.Sprintf("v%d", step.task.PlanVersion))
	base, err := step.base(step.ctx, step.project)
	if err != nil {
		return "", err
	}
	err = step.worktree(step.ctx, step.project.Path, path, fmt.Sprintf("codex/whirlwind-plan-%s-v%d", step.task.ID, step.task.PlanVersion), base)
	return path, err
}
func (step *taskStep) planningPrompt() (string, error) {
	data := map[string]string{"Title": step.task.Issue.Title, "Body": step.task.Issue.Body}
	if step.task.Plan != nil {
		data["Questions"] = jsonText(step.task.Plan.Questions)
	}
	return agents.Render("plan.md", data)
}

func decodePlan(text string) (model.Plan, error) {
	var plan model.Plan
	if err := provider.JSON(text, &plan); err != nil {
		return plan, err
	}
	if plan.Objective == "" || len(plan.AcceptanceCriteria) == 0 {
		return plan, errors.New("planner omitted objective or acceptance criteria")
	}
	if len(plan.Units) == 0 {
		plan.Units = []model.Unit{{ID: "main", Objective: plan.Objective}}
	}
	return plan, ValidateUnits(plan.Units)
}
func (step *taskStep) applyPlan(plan model.Plan) error {
	step.task.Plan = &plan
	step.task.Stage = "coding"
	if step.task.Issue.Number == 0 {
		step.task.Stage = "plan_ready"
	}
	for _, question := range plan.Questions {
		if question.ID == "" || question.Text == "" {
			return errors.New("invalid planner question")
		}
		if question.Answer == "" {
			step.task.Stage = "questions"
		}
	}
	return nil
}
