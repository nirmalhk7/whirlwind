package workflow

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

func (step *taskStep) planning() (bool, error) {
	plan, err := step.generatePlan()
	if err != nil {
		return true, err
	}
	if step.task.Stage != "questions" {
		if err := step.WriteLocalPlan(step.ctx, step.project, &step.task); err != nil {
			return true, err
		}
		return false, nil
	}
	step.task.Handoff.NextAction = "implement plan"
	key := fmt.Sprintf("%s:%d", step.task.ID, step.task.PlanVersion)
	previous, _ := step.Store.Get(step.ctx, "plans", key)
	step.pending = append(step.pending, store.Change{Kind: "plans", ID: key, Value: plan, Expected: previous.Version})
	return false, nil
}

func (step *taskStep) generatePlan() (model.Plan, error) {
	path, err := step.planningWorktree()
	if err != nil {
		return model.Plan{}, err
	}
	prompt, err := step.planningPrompt()
	if err != nil {
		return model.Plan{}, err
	}
	input, cleanup, err := step.planningIntegrationInput(path, prompt)
	if err != nil {
		return model.Plan{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	out, err := step.agent(step.ctx, step.project, step.task, input)
	if err != nil {
		return model.Plan{}, err
	}
	step.task.LastAttemptID = out.AttemptID
	return step.applyPlannerOutput(out.Text)
}

func (step *taskStep) planningIntegrationInput(path, prompt string) (provider.Input, func(), error) {
	input := provider.Input{Role: "plan", CWD: path, Prompt: prompt, ReadOnly: true, SessionID: ""}
	sources, err := step.promptSelectedIntegrationSources(step.ctx, "ideate")
	if err != nil {
		return provider.Input{}, nil, err
	}
	if len(sources) == 0 {
		return input, nil, nil
	}
	broker, err := step.createSelectedIntegrationBroker(step.ctx, step.project, sources)
	if err != nil {
		return provider.Input{}, nil, err
	}
	input.MCPBroker = broker.descriptor
	input.MCPBrokerToken = broker.token
	input.Prompt += "\nSelected read-only connection scopes available through the supervisor broker: " + jsonText(sources) + ". Do not attempt any external write or use another connection."
	return input, func() { _ = broker.close() }, nil
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
	branch := fmt.Sprintf("codex/whirlwind-plan-%s-v%d", step.task.ID, step.task.PlanVersion)
	snapshot, err := step.planningSnapshot(path, branch)
	if err != nil {
		return "", err
	}
	err = step.worktree(step.ctx, step.project.Path, snapshot.Path, snapshot.Branch, snapshot.Base)
	return snapshot.Path, err
}

type planningWorktreeSnapshot struct {
	Base   string
	Branch string
	Path   string
}

func (step *taskStep) planningSnapshot(path, branch string) (planningWorktreeSnapshot, error) {
	key := fmt.Sprintf("planning:%s:v%d", step.task.ID, step.task.PlanVersion)
	record, err := step.Store.Get(step.ctx, "workspaces", key)
	if err == nil {
		snapshot, decodeErr := store.Decode[planningWorktreeSnapshot](record)
		if decodeErr != nil {
			return planningWorktreeSnapshot{}, decodeErr
		}
		if snapshot.Path != path || snapshot.Branch != branch || snapshot.Base == "" {
			return planningWorktreeSnapshot{}, errors.New("persisted planning worktree identity does not match this plan version")
		}
		return snapshot, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return planningWorktreeSnapshot{}, err
	}
	base, err := step.base(step.ctx, step.project)
	if err != nil {
		return planningWorktreeSnapshot{}, err
	}
	snapshot := planningWorktreeSnapshot{Base: base, Branch: branch, Path: path}
	if err := step.Store.Put(step.ctx, "workspaces", key, snapshot, 0); err == nil {
		return snapshot, nil
	} else if !errors.Is(err, store.ErrConflict) {
		return planningWorktreeSnapshot{}, err
	}
	// A concurrent recovery can win the first durable snapshot write; always use it.
	record, err = step.Store.Get(step.ctx, "workspaces", key)
	if err != nil {
		return planningWorktreeSnapshot{}, err
	}
	snapshot, err = store.Decode[planningWorktreeSnapshot](record)
	if err != nil {
		return planningWorktreeSnapshot{}, err
	}
	if snapshot.Path != path || snapshot.Branch != branch || snapshot.Base == "" {
		return planningWorktreeSnapshot{}, errors.New("persisted planning worktree identity does not match this plan version")
	}
	return snapshot, nil
}
func (step *taskStep) planningPrompt() (string, error) {
	data := map[string]string{"Title": step.task.Issue.Title, "Body": step.task.Issue.Body}
	if step.task.Plan != nil {
		data["Questions"] = jsonText(step.task.Plan.Questions)
	}
	prompt, err := agents.Render("plan.md", data)
	return prompt + "\nUser selection authorization:\n" + jsonText(step.task.Selection) + "\nDecide implementation details within selected objective and project policy. Preserve the selected objective, define stable requirement IDs with exact outcomes and evidence methods, list exact repository commands found in committed configuration, and list checks. For each test/build command inventory, use `Configured: <source file and command>` when commands exist, or `None configured: <committed files inspected>` when none exist; do not invent commands. Include explicit scope, exclusions, and ordered implementation steps. Ask inbox questions for missing information. No plan may begin coding before human local approval. Mode: " + step.WorkflowConfiguration(step.ctx).ActiveMode + ".", err
}

func decodePlan(text string) (model.Plan, error) {
	var plan model.Plan
	if err := provider.JSON(text, &plan); err != nil {
		return plan, err
	}
	if plan.Objective == "" || len(plan.AcceptanceCriteria) == 0 {
		return plan, errors.New("planner omitted objective or acceptance criteria")
	}
	if strings.TrimSpace(plan.What) == "" || strings.TrimSpace(plan.Why) == "" || strings.TrimSpace(plan.Benefit) == "" || len(plan.Where) == 0 {
		return plan, errors.New("planner must explain what changes, why, product benefit, and affected implementation/test paths")
	}
	if err := validateDetailedPlan(plan); err != nil {
		return plan, err
	}
	if err := validatePlanRequirements(plan.Requirements); err != nil {
		return plan, err
	}
	if _, err := planVerificationChecks(model.Policy{}, plan); err != nil {
		return plan, err
	}
	for _, path := range plan.Where {
		if !safePlanDocumentPath(path) {
			return plan, errors.New("planner returned unsafe implementation path")
		}
	}
	if len(plan.Units) == 0 {
		plan.Units = []model.Unit{{ID: "main", Objective: plan.Objective}}
	}
	return plan, ValidateUnits(plan.Units)
}

func validateDetailedPlan(plan model.Plan) error {
	for label, entries := range map[string][]string{"scope": plan.Scope, "exclusions": plan.Exclusions, "implementation steps": plan.Steps} {
		if len(entries) == 0 {
			return fmt.Errorf("planner must provide explicit %s", label)
		}
		for _, entry := range entries {
			if strings.TrimSpace(entry) == "" {
				return fmt.Errorf("planner %s entries must not be empty", label)
			}
		}
	}
	for _, inventory := range []struct {
		label    string
		commands [][]string
		value    string
	}{{"repository test command inventory", plan.RepositoryTestCommands, plan.RepositoryTestInventory}, {"repository build command inventory", plan.RepositoryBuildCommands, plan.RepositoryBuildInventory}} {
		prefix := "configured:"
		if len(inventory.commands) == 0 {
			prefix = "none configured:"
		}
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(inventory.value)), prefix) || strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(inventory.value), prefix)) == "" {
			return fmt.Errorf("planner must document %s using %q with committed configuration evidence", inventory.label, prefix)
		}
	}
	if len(plan.Checks)+len(plan.RepositoryTestCommands)+len(plan.RepositoryBuildCommands) == 0 {
		return errors.New("planner must include plan checks or repository test/build commands")
	}
	return nil
}

func validatePlanRequirements(requirements []model.Requirement) error {
	if len(requirements) == 0 {
		return errors.New("planner must provide stable requirement IDs, outcomes, and evidence methods")
	}
	seen := map[string]bool{}
	for _, requirement := range requirements {
		if strings.TrimSpace(requirement.ID) == "" || strings.TrimSpace(requirement.Outcome) == "" || strings.TrimSpace(requirement.EvidenceMethod) == "" {
			return errors.New("each planner requirement needs a stable ID, exact outcome, and evidence method")
		}
		if seen[requirement.ID] {
			return fmt.Errorf("planner returned duplicate requirement ID %q", requirement.ID)
		}
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(requirement.EvidenceMethod)), "PENDING") {
			return fmt.Errorf("requirement %s needs a human-approved evidence method before coding", requirement.ID)
		}
		seen[requirement.ID] = true
	}
	return nil
}
func (step *taskStep) applyPlan(plan model.Plan) error {
	if err := ValidateUnitScopes(step.project, plan.Units); err != nil {
		return err
	}
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
