package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const maxLocalPlanBytes = 1024 * 1024

type PlanDocumentView struct {
	Path         string     `json:"path"`
	Hash         string     `json:"hash"`
	Content      string     `json:"content"`
	Plan         model.Plan `json:"plan"`
	ApprovedHash string     `json:"approved_hash,omitempty"`
}

// WriteLocalPlan prepares an approval document; the caller persists the task
// together with its other planning-stage changes. Existing edits are retained.
func (e *Engine) WriteLocalPlan(ctx context.Context, project model.Project, task *model.Task) error {
	if (model.EffectiveMode(task.Mode) != model.AgenticMode && model.EffectiveMode(task.Mode) != model.PersonalMode) || task.Plan == nil || task.ProjectID != project.ID {
		return errors.New("local plans require a configured task with a plan and matching project")
	}
	if task.PlanVersion < 1 {
		task.PlanVersion = 1
	}
	if len(task.Plan.Requirements) == 0 {
		task.Plan.Requirements = legacyPendingRequirements(task.Plan.AcceptanceCriteria)
	}
	if task.PlanDocument == nil {
		if err := e.restoreLocalPlanIdentity(ctx, project, task); err != nil {
			return err
		}
	}
	content, err := e.recoverLocalPlanContent(task, renderLocalPlanContent(*task))
	if err != nil {
		return err
	}
	if _, err := ParsePlanDocument(content); err != nil {
		return err
	}
	path, err := e.writeLocalPlanFile(*task, content)
	if err != nil {
		return err
	}
	task.PlanDocument.Path, task.PlanDocument.Hash = path, localPlanHash(content)
	task.Stage, task.ResumeStage, task.Error = "plan_approval", "", ""
	task.Handoff.NextAction = "Open the local plan in Inbox, review it, then approve, request revision, or reject."
	return e.Store.Notify(ctx, task.ID, "plan_approval", "Review local plan: "+path)
}

func legacyPendingRequirements(criteria []string) []model.Requirement {
	requirements := make([]model.Requirement, 0, len(criteria))
	for index, criterion := range criteria {
		if strings.TrimSpace(criterion) == "" {
			continue
		}
		requirements = append(requirements, model.Requirement{ID: fmt.Sprintf("LEGACY-%s-%d", localPlanHash(criterion)[:12], index+1), Outcome: criterion, EvidenceMethod: "PENDING HUMAN REVIEW: specify an evidence method before approving this migrated criterion."})
	}
	return requirements
}

func renderLocalPlanContent(task model.Task) string {
	content := RenderPlanDocument(*task.Plan)
	if len(task.Sources) == 0 {
		return content
	}
	content += "\n\n## Sources\n"
	for _, source := range task.Sources {
		if source.URL != "" {
			content += "\n- " + source.URL
		}
	}
	return content
}

func (e *Engine) recoverLocalPlanContent(task *model.Task, content string) (string, error) {
	root, relative, err := e.localPlanLocation(*task)
	if err != nil {
		return "", err
	}
	// A previous process may have published the document before its task write
	// committed. Recover that document, including manual edits, on planning retry.
	recovered := *task
	metadata := *task.PlanDocument
	metadata.Path = filepath.Join(root, relative)
	recovered.PlanDocument = &metadata
	existing, err := e.readLocalPlanFile(recovered)
	if os.IsNotExist(err) {
		return content, nil
	}
	if err != nil {
		return "", err
	}
	plan, err := ParsePlanDocument(existing)
	if err == nil {
		task.Plan = &plan
	}
	return existing, err
}

// ReadLocalPlan returns the current editable file and revokes stale approvals
// before exposing it as approved. Coding continues to consume task.Plan only.
func (e *Engine) ReadLocalPlan(ctx context.Context, id string) (PlanDocumentView, error) {
	record, task, err := e.localPlanForRead(ctx, id)
	if err != nil {
		return PlanDocumentView{}, err
	}
	content, err := e.readLocalPlanContent(ctx, record, &task)
	if err != nil {
		return PlanDocumentView{}, err
	}
	if err := e.refreshLocalPlanApproval(ctx, record, &task, content); err != nil {
		return PlanDocumentView{}, err
	}
	plan, err := ParsePlanDocument(content)
	view := PlanDocumentView{Path: task.PlanDocument.Path, Hash: localPlanHash(content), Content: content, Plan: plan, ApprovedHash: task.PlanDocument.ApprovedHash}
	return view, err
}

func (e *Engine) localPlanForRead(ctx context.Context, id string) (model.Record, model.Task, error) {
	record, err := e.Store.Get(ctx, "tasks", id)
	if err != nil {
		return record, model.Task{}, err
	}
	task, err := store.Decode[model.Task](record)
	if err != nil || task.PlanDocument == nil {
		return record, task, errors.New("task has no local plan")
	}
	return record, task, nil
}

func (e *Engine) readLocalPlanContent(ctx context.Context, record model.Record, task *model.Task) (string, error) {
	content, err := e.readLocalPlanFile(*task)
	if err != nil && localPlanApproved(*task) {
		if invalidationErr := e.invalidateLocalApproval(ctx, record, task, "", "Local plan is inaccessible; review is required."); invalidationErr != nil {
			return "", invalidationErr
		}
	}
	return content, err
}

func (e *Engine) refreshLocalPlanApproval(ctx context.Context, record model.Record, task *model.Task, content string) error {
	if !localPlanApproved(*task) {
		return nil
	}
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		return err
	}
	if localPlanHash(content) != task.PlanDocument.ApprovedHash || project.PolicyHash != task.PlanDocument.PolicyHash {
		return e.invalidateLocalApproval(ctx, record, task, content, "Local plan or project policy changed; review is required.")
	}
	return nil
}

func clearLocalPlanExecution(task *model.Task) {
	task.Approval, task.Submission, task.Wait = nil, nil, nil
	task.ChecksPassed, task.Approved, task.Score = false, false, 0
	task.VerificationID, task.ReviewID, task.ReviewedCommit = "", "", ""
	task.ImplementationAttempts, task.LastAttemptID = nil, ""
}

func (e *Engine) invalidateLocalApproval(ctx context.Context, record model.Record, task *model.Task, content, reason string) error {
	if localPlanFinished(*task) {
		return nil
	}
	var snapshotError error
	if content != "" {
		next := *task
		next.PlanVersion++
		if next.PlanDocument != nil && next.PlanDocument.PlanVersion != 0 {
			next.PlanDocument.PlanVersion = next.PlanVersion
		}
		path, err := e.writeLocalPlanFile(next, content)
		if err != nil {
			snapshotError = err
			reason += " Cannot save next document version: " + err.Error()
		} else {
			task.PlanVersion = next.PlanVersion
			task.PlanDocument.Path, task.PlanDocument.Hash = path, localPlanHash(content)
		}
	}
	task.PlanDocument.ApprovedHash, task.PlanDocument.PolicyHash, task.PlanDocument.ApprovedAt = "", "", ""
	clearLocalPlanExecution(task)
	task.Stage, task.ResumeStage, task.Error = "plan_approval", "", reason
	task.Handoff.NextAction = "Review the edited local plan in Inbox before execution resumes."
	notice := model.Notification{ID: store.ID(), TaskID: task.ID, Kind: "plan_approval", Message: reason, Current: "plan_approval", NextAction: task.Handoff.NextAction}
	if err := e.Store.Write(ctx, []store.Change{{Kind: "tasks", ID: task.ID, Value: *task, Expected: record.Version}, {Kind: "inbox", ID: notice.ID, Value: notice}}, nil); err != nil {
		return err
	}
	e.Stop(task.ID)
	return snapshotError
}

func (e *Engine) resolveLocalPlanNotices(ctx context.Context, id string) ([]store.Change, error) {
	records, err := e.Store.List(ctx, "inbox")
	if err != nil {
		return nil, err
	}
	var changes []store.Change
	for _, record := range records {
		notice, err := store.Decode[model.Notification](record)
		if err != nil {
			return nil, err
		}
		if notice.TaskID == id && notice.Kind == "plan_approval" && !notice.Resolved {
			notice.Resolved = true
			changes = append(changes, store.Change{Kind: "inbox", ID: record.ID, Value: notice, Expected: record.Version})
		}
	}
	return changes, nil
}

func (e *Engine) refreshLocalApprovals(ctx context.Context) {
	rows, err := e.Store.List(ctx, "tasks")
	if err != nil {
		return
	}
	for _, r := range rows {
		t, err := store.Decode[model.Task](r)
		if err != nil || localPlanFinished(t) || !localPlanApproved(t) {
			continue
		}
		if err := e.ValidateLocalPlan(ctx, t); err != nil {
			e.Stop(t.ID)
		}
	}
}

func localPlanFinished(task model.Task) bool {
	return task.Cancelled() || task.Stage == "complete" || task.Stage == "pr_created"
}
