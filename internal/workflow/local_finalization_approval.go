package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type LocalFinalizationApproval struct {
	PlanVersion      int    `json:"plan_version"`
	ApprovedPlanHash string `json:"approved_plan_hash"`
	ReviewedCommit   string `json:"reviewed_commit"`
	ReviewID         string `json:"review_id"`
	VerificationID   string `json:"verification_id"`
	PolicyHash       string `json:"policy_hash"`
}

func (e *Engine) ApproveLocalFinalization(ctx context.Context, taskID string, expectedTaskVersion int, approval LocalFinalizationApproval, idempotencyKey string) (model.Task, error) {
	if !validFinalizationKey(idempotencyKey) {
		return model.Task{}, errors.New("local finalization approval requires a printable idempotency key of at most 128 characters")
	}
	fingerprint := localFinalizationFingerprint(taskID, expectedTaskVersion, approval)
	status, body, err := e.Store.Idempotent(ctx, idempotencyKey, fingerprint)
	if err != nil {
		return model.Task{}, err
	}
	if status != 0 {
		return decodeFinalizationReceipt(body)
	}
	if strings.TrimSpace(taskID) == "" || expectedTaskVersion < 1 {
		return model.Task{}, errors.New("local finalization approval requires a task ID and expected task version")
	}
	if err := validateFinalizationTuple(approval); err != nil {
		return model.Task{}, err
	}
	taskRecord, err := e.Store.Get(ctx, "tasks", taskID)
	if err != nil {
		return model.Task{}, err
	}
	if taskRecord.Version != expectedTaskVersion {
		return model.Task{}, store.ErrConflict
	}
	task, err := store.Decode[model.Task](taskRecord)
	if err != nil {
		return model.Task{}, err
	}
	if task.Stage != "approval" || task.Risk != "critical" || task.Approved || task.Submission != nil || task.Delivery != nil {
		return model.Task{}, errors.New("task is not awaiting critical local-finalization approval")
	}
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		return model.Task{}, err
	}
	if err = validateFinalizationPlan(e, task, approval); err != nil {
		return model.Task{}, err
	}
	review, report, accepted, err := e.LoadAcceptedReviewReport(ctx, taskID)
	if err != nil {
		return model.Task{}, err
	}
	if !accepted || !finalizationEvidenceMatches(task, review, report, approval, project.PolicyHash) {
		return model.Task{}, errors.New("accepted review evidence no longer matches the displayed critical candidate")
	}
	proof, err := validateFinalizationCandidate(ctx, task, approval)
	if err != nil {
		return model.Task{}, err
	}
	task.Approved, task.Approval = true, &proof
	task.Stage = "submitting"
	task.Handoff.NextAction = "stage the human-approved exact candidate for local finalization"
	encoded, err := json.Marshal(task)
	if err != nil {
		return model.Task{}, err
	}
	audit := model.Audit{Task: task.ID, Source: "local_operator", Action: "approve_local_finalization", Target: task.ID, Version: taskRecord.Version, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Evidence: &proof}
	receipt := &store.Receipt{Key: idempotencyKey, Fingerprint: fingerprint, Status: 200, Body: encoded}
	changes := []store.Change{{Kind: "tasks", ID: task.ID, Value: task, Expected: taskRecord.Version}, {Kind: "audit", ID: store.ID(), Value: audit}}
	if err = e.Store.Write(ctx, changes, receipt); err != nil {
		if errors.Is(err, store.ErrConflict) {
			if replay, replayErr := e.replayFinalizationApproval(ctx, idempotencyKey, fingerprint); replayErr == nil && replay != nil {
				return *replay, nil
			}
		}
		return model.Task{}, err
	}
	return task, nil
}

func validFinalizationKey(key string) bool {
	if key == "" || len(key) > 128 {
		return false
	}
	for _, char := range key {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

func validateFinalizationTuple(approval LocalFinalizationApproval) error {
	if approval.PlanVersion < 1 || strings.TrimSpace(approval.ApprovedPlanHash) == "" || strings.TrimSpace(approval.ReviewedCommit) == "" || strings.TrimSpace(approval.ReviewID) == "" || strings.TrimSpace(approval.VerificationID) == "" || strings.TrimSpace(approval.PolicyHash) == "" {
		return errors.New("local finalization approval requires the exact approved plan, candidate, review, verification, and policy")
	}
	return nil
}

func validateFinalizationPlan(e *Engine, task model.Task, approval LocalFinalizationApproval) error {
	if task.PlanDocument == nil || task.Plan == nil || task.PlanVersion != approval.PlanVersion || task.PlanDocument.PlanVersion != approval.PlanVersion || task.PlanDocument.ApprovedHash != approval.ApprovedPlanHash || task.PlanDocument.Hash != approval.ApprovedPlanHash {
		return errors.New("approved plan version or hash changed after the approval form was displayed")
	}
	content, err := e.readLocalPlanFile(task)
	if err != nil {
		return err
	}
	if localPlanHash(content) != approval.ApprovedPlanHash {
		return errors.New("approved plan file changed after the approval form was displayed")
	}
	current, err := ParsePlanDocument(content)
	if err != nil {
		return err
	}
	if !sameApprovedPlanSpecification(*task.Plan, current) {
		return errors.New("approved plan contents differ from the immutable reviewed specification")
	}
	return nil
}

func finalizationEvidenceMatches(task model.Task, review model.Review, report model.Adherence, approval LocalFinalizationApproval, policyHash string) bool {
	provenance := report.Provenance
	return policyHash == approval.PolicyHash && task.ReviewedCommit == approval.ReviewedCommit && task.ReviewID == approval.ReviewID && task.VerificationID == approval.VerificationID &&
		provenance.Task == task.ID && provenance.PlanVersion == approval.PlanVersion && provenance.Head == approval.ReviewedCommit && provenance.Base == task.Base && provenance.PolicyHash == approval.PolicyHash && provenance.VerificationID == approval.VerificationID &&
		review.Provenance != nil && review.Provenance.ReviewID == approval.ReviewID && review.Provenance.VerificationID == approval.VerificationID && review.Provenance.Head == approval.ReviewedCommit && review.Provenance.PlanVersion == approval.PlanVersion
}

func validateFinalizationCandidate(ctx context.Context, task model.Task, approval LocalFinalizationApproval) (model.Provenance, error) {
	if task.Worktree == "" || task.Base == "" {
		return model.Provenance{}, errors.New("critical candidate worktree or base is missing")
	}
	if err := submissionClean(ctx, task.Worktree); err != nil {
		return model.Provenance{}, fmt.Errorf("critical candidate must be clean before approval: %w", err)
	}
	head, err := submissionCommit(ctx, task.Worktree, "HEAD")
	if err != nil {
		return model.Provenance{}, err
	}
	if head != approval.ReviewedCommit {
		return model.Provenance{}, errors.New("candidate HEAD changed after the approval form was displayed")
	}
	base, err := submissionCommit(ctx, task.Worktree, task.Base)
	if err != nil {
		return model.Provenance{}, err
	}
	if _, err = submissionGitRun(ctx, task.Worktree, "merge-base", "--is-ancestor", base, head); err != nil {
		return model.Provenance{}, errors.New("reviewed candidate is not based on the approved task base")
	}
	return model.Provenance{Task: task.ID, PlanVersion: approval.PlanVersion, Head: approval.ReviewedCommit, Base: task.Base, PolicyHash: approval.PolicyHash, VerificationID: approval.VerificationID, ReviewID: approval.ReviewID}, nil
}

func (e *Engine) replayFinalizationApproval(ctx context.Context, key, fingerprint string) (*model.Task, error) {
	status, body, err := e.Store.Idempotent(ctx, key, fingerprint)
	if err != nil || status == 0 {
		return nil, err
	}
	task, err := decodeFinalizationReceipt(body)
	return &task, err
}

func decodeFinalizationReceipt(body []byte) (model.Task, error) {
	var task model.Task
	if err := json.Unmarshal(body, &task); err != nil {
		return model.Task{}, fmt.Errorf("decode local finalization approval receipt: %w", err)
	}
	return task, nil
}

func localFinalizationFingerprint(taskID string, expectedTaskVersion int, approval LocalFinalizationApproval) string {
	request := struct {
		Action  string                    `json:"action"`
		TaskID  string                    `json:"task_id"`
		Version int                       `json:"expected_task_version"`
		Plan    LocalFinalizationApproval `json:"approval"`
	}{Action: "approve_local_finalization", TaskID: taskID, Version: expectedTaskVersion, Plan: approval}
	encoded, _ := json.Marshal(request)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
