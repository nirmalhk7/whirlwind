package workflow

import (
	"context"
	"errors"
	"fmt"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// SaveLocalPlan publishes a new unapproved document. Previous versions and
// delivered branches remain available; edits to approved plans wait until
// their active execution finishes or is explicitly paused.
func (e *Engine) SaveLocalPlan(ctx context.Context, id string, expected int, hash, content string) error {
	record, task, err := e.localPlanForRead(ctx, id)
	if err != nil {
		return err
	}
	if record.Version != expected || hash == "" || task.Cancelled() {
		return store.ErrConflict
	}
	old, err := e.readLocalPlanFile(task)
	if err != nil {
		return err
	}
	if localPlanHash(old) != hash {
		return store.ErrConflict
	}
	if localPlanApproved(task) && activeApprovedPlanStage(task.Stage) {
		if content == old {
			return nil
		}
		return fmt.Errorf("%w: approved plan version is still active; wait until it finishes or explicitly pause it before saving changes", store.ErrConflict)
	}
	plan, err := parseEditedLocalPlan(content)
	if err != nil {
		return err
	}
	project, err := e.localPlanApprovalProject(ctx, task.ProjectID, plan)
	if err != nil {
		return err
	}
	if task.IdeaID != "" {
		if err := validatePlanCommandReferences(project.Policy, plan); err != nil {
			return err
		}
	}
	task.PlanVersion++
	task.PlanDocument = nil
	if err := e.restoreLocalPlanIdentity(ctx, project, &task); err != nil {
		return err
	}
	path, err := e.writeLocalPlanFile(task, content)
	if err != nil {
		return err
	}
	task.Plan = &plan
	task.PlanDocument.Path = path
	task.PlanDocument.Hash = localPlanHash(content)
	clearLocalPlanExecution(&task)
	task.Stage, task.ResumeStage, task.Error = "plan_approval", "", ""
	task.Handoff.NextAction = "Review and approve the saved document before execution."
	if err := e.Store.Put(ctx, "execution_plans", id, task, expected); err != nil {
		return err
	}
	store.AfterCommit(ctx, func() { e.Stop(id) })
	return nil
}

func activeApprovedPlanStage(stage string) bool {
	switch stage {
	case "coding", "review", "repair", "publishing", "submitting", "waiting_quota":
		return true
	default:
		return false
	}
}

func parseEditedLocalPlan(content string) (model.PlanSpecification, error) {
	if len(content) > maxLocalPlanBytes {
		return model.PlanSpecification{}, errors.New("local plan exceeds 1 MiB")
	}
	plan, err := ParsePlanDocument(content)
	if err != nil {
		return model.PlanSpecification{}, err
	}
	if err := validatePlanRequirements(plan.Requirements); err != nil {
		return model.PlanSpecification{}, err
	}
	if err := validateDetailedPlan(plan); err != nil {
		return model.PlanSpecification{}, err
	}
	return plan, nil
}
