package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
)

// ReviewLocalPlan binds approval to exact content, task version, and policy.
func (e *Engine) ReviewLocalPlan(ctx context.Context, id string, expected int, action, hash, feedback string) error {
	task, err := e.localPlanForReview(ctx, id, expected)
	if err != nil {
		return err
	}
	changes, err := e.reviewLocalPlanAction(ctx, &task, action, hash, feedback)
	if err != nil {
		return err
	}
	changes = append(changes, store.Change{Kind: "tasks", ID: id, Value: task, Expected: expected})
	resolved, err := e.resolveLocalPlanNotices(ctx, id)
	if err != nil {
		return err
	}
	if err := e.Store.Write(ctx, append(changes, resolved...), nil); err != nil {
		return err
	}
	if action != "approve" {
		e.Stop(id)
	}
	return nil
}

func (e *Engine) localPlanForReview(ctx context.Context, id string, expected int) (model.Task, error) {
	record, err := e.Store.Get(ctx, "tasks", id)
	if err != nil {
		return model.Task{}, err
	}
	if record.Version != expected {
		return model.Task{}, store.ErrConflict
	}
	task, err := store.Decode[model.Task](record)
	if err != nil {
		return task, err
	}
	if (model.EffectiveMode(task.Mode) != model.AgenticMode && model.EffectiveMode(task.Mode) != model.PersonalMode) || task.Stage != "plan_approval" || task.PlanDocument == nil {
		return task, store.ErrConflict
	}
	return task, nil
}

func (e *Engine) reviewLocalPlanAction(ctx context.Context, task *model.Task, action, hash, feedback string) ([]store.Change, error) {
	switch action {
	case "approve":
		return e.approveLocalPlan(ctx, task, hash)
	case "revise":
		return nil, reviseLocalPlan(task, feedback)
	case "reject":
		task.Stage, task.ResumeStage, task.Error = "cancelled", "", "Local plan rejected."
		task.PlanDocument.ApprovedHash, task.PlanDocument.PolicyHash, task.PlanDocument.ApprovedAt = "", "", ""
		clearLocalPlanExecution(task)
		return nil, nil
	default:
		return nil, errors.New("local plan action must be approve, revise, or reject")
	}
}

func (e *Engine) approveLocalPlan(ctx context.Context, task *model.Task, hash string) ([]store.Change, error) {
	content, plan, err := e.readLocalPlanApproval(*task, hash)
	if err != nil {
		return nil, err
	}
	if err := validatePlanRequirements(plan.Requirements); err != nil {
		return nil, fmt.Errorf("plan approval requires complete frozen requirements: %w", err)
	}
	if err := validateDetailedPlan(plan); err != nil {
		return nil, fmt.Errorf("plan approval requires a complete implementation specification: %w", err)
	}
	project, err := e.localPlanApprovalProject(ctx, task.ProjectID, plan)
	if err != nil {
		return nil, err
	}
	if err := e.freshLocalPlanVersion(ctx, task, content); err != nil {
		return nil, err
	}
	task.Plan = &plan
	task.PlanDocument.Hash, task.PlanDocument.ApprovedHash = hash, hash
	task.PlanDocument.PolicyHash = project.PolicyHash
	task.PlanDocument.ApprovedAt = time.Now().UTC().Format(time.RFC3339Nano)
	task.Stage, task.ResumeStage, task.Error = "coding", "", ""
	task.Handoff.NextAction = "Implement the immutable approved local plan."
	return []store.Change{{Kind: "plans", ID: fmt.Sprintf("%s:%d", task.ID, task.PlanVersion), Value: plan}}, nil
}

func (e *Engine) readLocalPlanApproval(task model.Task, hash string) (string, model.Plan, error) {
	content, err := e.readLocalPlanFile(task)
	if err != nil {
		return "", model.Plan{}, err
	}
	if hash == "" || hash != localPlanHash(content) {
		return "", model.Plan{}, store.ErrConflict
	}
	plan, err := ParsePlanDocument(content)
	return content, plan, err
}

func (e *Engine) localPlanApprovalProject(ctx context.Context, id string, plan model.Plan) (model.Project, error) {
	project, err := e.Project(ctx, id)
	if err != nil {
		return project, err
	}
	for _, file := range plan.Where {
		if !policy.AllowedPath(project.Policy, file) {
			return project, fmt.Errorf("local plan path is outside project policy: %s", file)
		}
	}
	return project, nil
}

func (e *Engine) freshLocalPlanVersion(ctx context.Context, task *model.Task, content string) error {
	_, err := e.Store.Get(ctx, "plans", fmt.Sprintf("%s:%d", task.ID, task.PlanVersion))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// A revoked approval may still own this immutable artifact, for example
	// after an inaccessible file is restored. Allocate a fresh version.
	task.PlanVersion++
	if task.PlanDocument != nil && task.PlanDocument.PlanVersion != 0 {
		task.PlanDocument.PlanVersion = task.PlanVersion
	}
	path, err := e.writeLocalPlanFile(*task, content)
	if err == nil {
		task.PlanDocument.Path = path
	}
	return err
}

func reviseLocalPlan(task *model.Task, feedback string) error {
	if strings.TrimSpace(feedback) == "" {
		return errors.New("revision feedback is required")
	}
	task.PlanVersion++
	task.PlanDocument, task.Plan = nil, nil
	task.Issue.Body += "\n\nPlan revision feedback:\n" + strings.TrimSpace(feedback)
	task.Stage, task.ResumeStage, task.Error = "planning", "", ""
	clearLocalPlanExecution(task)
	return nil
}

func (e *Engine) ValidateLocalPlan(ctx context.Context, task model.Task) error {
	if model.EffectiveMode(task.Mode) != model.AgenticMode && model.EffectiveMode(task.Mode) != model.PersonalMode {
		return nil
	}
	project, projectErr := e.trustedProject(ctx, task.ProjectID)
	if localPlanPolicyStale(task, project.PolicyHash, projectErr) {
		e.invalidateLocalPlanPolicy(ctx, task.ID, project.PolicyHash, projectErr)
		return errors.New("local plan policy approval is stale")
	}
	if task.PlanDocument == nil {
		return e.requireLegacyPlanApproval(ctx, project, task.ID)
	}
	view, err := e.ReadLocalPlan(ctx, task.ID)
	if err != nil {
		return err
	}
	if err := validatePlanRequirements(view.Plan.Requirements); err != nil {
		record, readErr := e.Store.Get(ctx, "tasks", task.ID)
		if readErr != nil {
			return readErr
		}
		current, decodeErr := store.Decode[model.Task](record)
		if decodeErr != nil {
			return decodeErr
		}
		if localPlanApproved(current) {
			if invalidateErr := e.invalidateLocalApproval(ctx, record, &current, "", "Legacy plan requirements lack stable evidence methods; add them and approve the plan again."); invalidateErr != nil {
				return invalidateErr
			}
		}
		return errors.New("local plan requires human review of stable requirement IDs and evidence methods")
	}
	return e.validateLocalPlanSnapshot(ctx, task, view.Hash)
}

func (e *Engine) requireLegacyPlanApproval(ctx context.Context, project model.Project, id string) error {
	record, err := e.Store.Get(ctx, "tasks", id)
	if err != nil {
		return err
	}
	task, err := store.Decode[model.Task](record)
	if err != nil {
		return err
	}
	if task.Plan == nil {
		return errors.New("coding cannot start: the legacy task has no recoverable plan; inspect its saved history")
	}
	task.Mode = model.EffectiveMode(task.Mode)
	if task.PlanVersion < 1 {
		task.PlanVersion = 1
	}
	if err := e.WriteLocalPlan(ctx, project, &task); err != nil {
		return err
	}
	if err := e.Store.Put(ctx, "tasks", id, task, record.Version); err != nil {
		return err
	}
	return nil
}

func localPlanPolicyStale(task model.Task, policyHash string, projectErr error) bool {
	return projectErr != nil || task.PlanDocument != nil && task.PlanDocument.PolicyHash != policyHash
}

func (e *Engine) invalidateLocalPlanPolicy(ctx context.Context, id, policyHash string, projectErr error) {
	record, err := e.Store.Get(ctx, "tasks", id)
	if err != nil {
		return
	}
	current, err := store.Decode[model.Task](record)
	if err != nil || !localPlanApproved(current) {
		return
	}
	if localPlanPolicyStale(current, policyHash, projectErr) {
		_ = e.invalidateLocalApproval(ctx, record, &current, "", "Project policy changed; review the local plan against current policy.")
	}
}

func (e *Engine) validateLocalPlanSnapshot(ctx context.Context, task model.Task, hash string) error {
	current, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		return err
	}
	stored, err := store.Decode[model.Task](current)
	if err != nil {
		return err
	}
	if !localPlanSnapshotMatches(stored, task, hash) {
		return errors.New("local plan approval is missing or stale; review the current document in Inbox")
	}
	return nil
}

func localPlanSnapshotMatches(stored, task model.Task, hash string) bool {
	if stored.Cancelled() || stored.PlanVersion != task.PlanVersion || !localPlanApproved(stored) {
		return false
	}
	return stored.PlanDocument.ApprovedHash == hash && task.PlanDocument != nil && task.PlanDocument.ApprovedHash == hash
}

func localPlanApproved(task model.Task) bool {
	return task.PlanDocument != nil && task.PlanDocument.ApprovedHash != ""
}
