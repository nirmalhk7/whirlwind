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
	changes = append(changes, store.Change{Kind: "execution_plans", ID: id, Value: task, Expected: expected})
	resolved, err := e.resolveLocalPlanNotices(ctx, id)
	if err != nil {
		return err
	}
	if err := e.Store.Write(ctx, append(changes, resolved...), nil); err != nil {
		return err
	}
	if action != "approve" {
		store.AfterCommit(ctx, func() { e.Stop(id) })
	}
	return nil
}

func (e *Engine) localPlanForReview(ctx context.Context, id string, expected int) (model.ExecutionPlan, error) {
	record, err := e.Store.Get(ctx, "execution_plans", id)
	if err != nil {
		return model.ExecutionPlan{}, err
	}
	if record.Version != expected {
		return model.ExecutionPlan{}, store.ErrConflict
	}
	task, err := store.Decode[model.ExecutionPlan](record)
	if err != nil {
		return task, err
	}
	if (model.EffectiveMode(task.Mode) != model.AgenticMode && model.EffectiveMode(task.Mode) != model.PersonalMode) || task.Stage != "plan_approval" || task.PlanDocument == nil {
		return task, store.ErrConflict
	}
	return task, nil
}

func (e *Engine) reviewLocalPlanAction(ctx context.Context, task *model.ExecutionPlan, action, hash, feedback string) ([]store.Change, error) {
	switch action {
	case "approve":
		return e.approveLocalPlan(ctx, task, hash)
	case "revise":
		if task.IdeaID != "" {
			if strings.TrimSpace(feedback) == "" {
				return nil, errors.New("revision feedback is required")
			}
			task.Stage, task.ResumeStage, task.Error = "plan_approval", "", strings.TrimSpace(feedback)
			task.PlanDocument.ApprovedHash, task.PlanDocument.PolicyHash, task.PlanDocument.ApprovedAt = "", "", ""
			clearLocalPlanExecution(task)
			task.Handoff.NextAction = "Edit the saved Markdown and save a new unapproved version, then approve it separately. Planner authoring remains completed."
			return nil, nil
		}
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

func (e *Engine) approveLocalPlan(ctx context.Context, task *model.ExecutionPlan, hash string) ([]store.Change, error) {
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
	project, projectVersion, err := e.localPlanApprovalProject(ctx, task.ProjectID, plan)
	if err != nil {
		return nil, err
	}
	if project.ManagedPolicy && project.Status != "approved" {
		return nil, errors.New("finish project setup before approving its execution plan")
	}
	if task.IdeaID != "" {
		if err := validatePairCapacity(e.settings(ctx), project); err != nil {
			return nil, err
		}
		if err := validatePlanCommandReferences(project.Policy, plan); err != nil {
			return nil, err
		}
	}
	if err := e.freshLocalPlanVersion(ctx, task, content); err != nil {
		return nil, err
	}
	references, guards, err := e.approvalConfiguration(ctx, task.ProjectID)
	if err != nil {
		return nil, err
	}
	if project.ManagedPolicy && references[0].Version != projectVersion {
		return nil, store.ErrConflict
	}
	plan.Approval = &model.PlanApproval{DocumentHash: hash, PlanVersion: task.PlanVersion, References: references, ConfigurationHash: evidenceDigest(references)}
	plan.Specification = content
	task.Plan = &plan
	task.PlanDocument.Hash, task.PlanDocument.ApprovedHash = hash, hash
	task.PlanDocument.PolicyHash = project.PolicyHash
	task.PlanDocument.ApprovedAt = time.Now().UTC().Format(time.RFC3339Nano)
	task.Stage, task.ResumeStage, task.Error = "coding", "", ""
	task.Handoff.NextAction = "Implement the immutable approved local plan."
	return append(guards, store.Change{Kind: "plan_versions", ID: fmt.Sprintf("%s:%d", task.ID, task.PlanVersion), Value: plan}), nil
}

func (e *Engine) readLocalPlanApproval(task model.ExecutionPlan, hash string) (string, model.PlanSpecification, error) {
	content, err := e.readLocalPlanFile(task)
	if err != nil {
		return "", model.PlanSpecification{}, err
	}
	if hash == "" || hash != localPlanHash(content) {
		return "", model.PlanSpecification{}, store.ErrConflict
	}
	plan, err := ParsePlanDocument(content)
	return content, plan, err
}

func (e *Engine) localPlanApprovalProject(ctx context.Context, id string, plan model.PlanSpecification) (model.Project, int, error) {
	record, err := e.Store.Get(ctx, "projects", id)
	if err != nil {
		return model.Project{}, 0, err
	}
	project, err := store.Decode[model.Project](record)
	if err != nil {
		return project, record.Version, err
	}
	if project.ManagedPolicy || project.PolicyHash == "" {
		project.Policy, err = e.projectPolicy(ctx, project)
		if err != nil {
			return project, record.Version, err
		}
	}
	for _, file := range plan.Where {
		if !policy.AllowedPath(project.Policy, file) {
			return project, record.Version, fmt.Errorf("local plan path is outside project policy: %s", file)
		}
	}
	return project, record.Version, nil
}

func (e *Engine) freshLocalPlanVersion(ctx context.Context, task *model.ExecutionPlan, content string) error {
	_, err := e.Store.Get(ctx, "plan_versions", fmt.Sprintf("%s:%d", task.ID, task.PlanVersion))
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

func reviseLocalPlan(task *model.ExecutionPlan, feedback string) error {
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

func (e *Engine) ValidateLocalPlan(ctx context.Context, task model.ExecutionPlan) error {
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
	record, current, err := e.localPlanForRead(ctx, task.ID)
	if err != nil {
		return err
	}
	if err := e.refreshLocalPlanApproval(ctx, record, &current); err != nil {
		return err
	}
	frozen, err := e.approvedLocalPlanSnapshot(ctx, current)
	if err != nil {
		return err
	}
	return e.validateLocalPlanSnapshot(ctx, task, localPlanHash(frozen.Specification))
}

func (e *Engine) requireLegacyPlanApproval(ctx context.Context, project model.Project, id string) error {
	record, err := e.Store.Get(ctx, "execution_plans", id)
	if err != nil {
		return err
	}
	task, err := store.Decode[model.ExecutionPlan](record)
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
	if err := e.Store.Put(ctx, "execution_plans", id, task, record.Version); err != nil {
		return err
	}
	return nil
}

func localPlanPolicyStale(task model.ExecutionPlan, policyHash string, projectErr error) bool {
	return projectErr != nil || task.PlanDocument != nil && task.PlanDocument.PolicyHash != policyHash
}

func (e *Engine) invalidateLocalPlanPolicy(ctx context.Context, id, policyHash string, projectErr error) {
	record, err := e.Store.Get(ctx, "execution_plans", id)
	if err != nil {
		return
	}
	current, err := store.Decode[model.ExecutionPlan](record)
	if err != nil || !localPlanApproved(current) {
		return
	}
	if localPlanPolicyStale(current, policyHash, projectErr) {
		_ = e.invalidateLocalApproval(ctx, record, &current, "Project policy changed; review the local plan against current policy.")
	}
}

func (e *Engine) validateLocalPlanSnapshot(ctx context.Context, task model.ExecutionPlan, hash string) error {
	current, err := e.Store.Get(ctx, "execution_plans", task.ID)
	if err != nil {
		return err
	}
	stored, err := store.Decode[model.ExecutionPlan](current)
	if err != nil {
		return err
	}
	if !localPlanSnapshotMatches(stored, task, hash) {
		return errors.New("local plan approval is missing or stale; review the current document in Inbox")
	}
	return nil
}

func localPlanSnapshotMatches(stored, task model.ExecutionPlan, hash string) bool {
	if stored.Cancelled() || stored.PlanVersion != task.PlanVersion || !localPlanApproved(stored) {
		return false
	}
	return stored.PlanDocument.ApprovedHash == hash && task.PlanDocument != nil && task.PlanDocument.ApprovedHash == hash && stored.Plan != nil && task.Plan != nil && sameApprovedPlanSpecification(*stored.Plan, *task.Plan)
}

func localPlanApproved(task model.ExecutionPlan) bool {
	return task.PlanDocument != nil && task.PlanDocument.ApprovedHash != ""
}

// approvedLocalPlanSnapshot validates durable approved bytes, never the editable
// Markdown path. Runtime unit progress is excluded by loadFrozenPlan.
func (e *Engine) approvedLocalPlanSnapshot(ctx context.Context, task model.ExecutionPlan) (model.PlanSpecification, error) {
	if !localPlanApproved(task) || task.Plan == nil {
		return model.PlanSpecification{}, errors.New("local plan approval is missing or stale; review the current document in Inbox")
	}
	frozen, err := e.loadFrozenPlan(ctx, task)
	if err != nil {
		return frozen, err
	}
	if frozen.Approval == nil || frozen.Approval.PlanVersion != task.PlanVersion || frozen.Approval.DocumentHash != task.PlanDocument.ApprovedHash || localPlanHash(frozen.Specification) != task.PlanDocument.ApprovedHash {
		return frozen, errors.New("immutable local plan approval identity is invalid")
	}
	if err := validatePlanRequirements(frozen.Requirements); err != nil {
		return frozen, err
	}
	return frozen, nil
}
