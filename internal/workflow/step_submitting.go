package workflow

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (step *taskStep) submitting() (bool, error) {
	if step.task.Delivery == nil {
		return step.prepareLocalDelivery()
	}
	if _, accepted, err := step.acceptedDeliveryReport(); err != nil || !accepted {
		if err != nil {
			return true, err
		}
		return true, errors.New("local delivery requires accepted review and verification for the exact candidate")
	}
	delivery := step.task.Delivery
	switch delivery.Stage {
	case "prepared":
		commit, err := promoteReviewedSnapshot(step.ctx, delivery.OriginalWorktree, delivery.Worktree, delivery.Branch, delivery.Base, delivery.ReviewedCommit)
		if err != nil {
			return true, err
		}
		if commit != delivery.ReviewedCommit {
			return true, errors.New("local delivery promotion changed the reviewed commit")
		}
		delivery.Stage = "snapshot_ready"
		return false, nil
	case "snapshot_ready":
		return step.finishLocalDelivery()
	case "complete":
		step.task.Stage = "complete"
		return true, step.Store.Put(step.ctx, "tasks", step.task.ID, step.task, step.record.Version)
	default:
		return true, fmt.Errorf("unknown local delivery state %q", delivery.Stage)
	}
}

func (step *taskStep) acceptedDeliveryReport() (model.Adherence, bool, error) {
	review, report, accepted, err := step.LoadAcceptedReviewReport(step.ctx, step.task.ID)
	if err != nil {
		return report, false, err
	}
	provenance := report.Provenance
	if !accepted || provenance.Task != step.task.ID || provenance.PlanVersion != step.task.PlanVersion ||
		provenance.Head == "" || provenance.Head != step.task.ReviewedCommit || provenance.Base != step.task.Base ||
		provenance.PolicyHash != step.project.PolicyHash || provenance.VerificationID != step.task.VerificationID ||
		review.Provenance == nil || review.Provenance.ReviewID != step.task.ReviewID {
		return report, false, nil
	}
	if step.task.Risk == "critical" && !approvalMatches(step.task, step.project.PolicyHash) {
		return report, false, nil
	}
	return report, true, nil
}

func (step *taskStep) prepareLocalDelivery() (bool, error) {
	report, accepted, err := step.acceptedDeliveryReport()
	if err != nil {
		return true, err
	}
	if !accepted {
		return true, errors.New("local delivery requires accepted review and verification for the exact candidate")
	}
	if step.task.Worktree == "" || step.task.ReviewedCommit == "" || step.task.Base == "" {
		return true, errors.New("local delivery is missing its reviewed candidate snapshot")
	}
	tree, err := submissionGitRun(step.ctx, step.task.Worktree, "rev-parse", step.task.ReviewedCommit+submissionTree)
	if err != nil {
		return true, err
	}
	diff, err := submissionGitRun(step.ctx, step.task.Worktree, "diff", "--binary", step.task.Base+"..."+step.task.ReviewedCommit)
	if err != nil {
		return true, err
	}
	key := deliveryKey(step.task.ID, step.task.PlanVersion)
	unmet := deliveryUnmetRequirements(report)
	step.task.Delivery = &model.LocalDelivery{
		UnmetRequirements: unmet,
		Adherence:         &report,
		VerificationID:    step.task.VerificationID,
		OriginalWorktree:  step.task.Worktree,
		OriginalBranch:    step.task.Branch,
		Worktree:          filepath.Join(filepath.Dir(step.task.Worktree), "delivery-"+key),
		Branch:            "codex/delivery/" + key,
		ReviewedCommit:    report.Provenance.Head,
		Base:              report.Provenance.Base,
		Tree:              tree,
		ReviewID:          report.Provenance.ReviewID,
		PolicyHash:        report.Provenance.PolicyHash,
		PlanVersion:       step.task.PlanVersion,
		Score:             report.Score,
		Diff:              diff,
		Stage:             "prepared",
	}
	step.task.Handoff.NextAction = "Prepare the exact accepted candidate for local delivery"
	return false, nil
}

func deliveryUnmetRequirements(report model.Adherence) []string {
	var unmet []string
	for _, result := range report.Results {
		if result.Status == "pass" {
			continue
		}
		label := result.ID
		if result.Explanation != "" {
			label += ": " + result.Explanation
		}
		unmet = append(unmet, label)
	}
	return unmet
}

func deliveryKey(taskID string, planVersion int) string {
	sum := sha256.Sum256([]byte(taskID))
	return fmt.Sprintf("task-%x-v%d", sum[:8], planVersion)
}

func (step *taskStep) finishLocalDelivery() (bool, error) {
	delivery := step.task.Delivery
	if _, err := promoteReviewedSnapshot(step.ctx, delivery.OriginalWorktree, delivery.Worktree, delivery.Branch, delivery.Base, delivery.ReviewedCommit); err != nil {
		return true, err
	}
	prompt := "You are the local delivery finalizer. Read the accepted plan, review evidence and exact candidate diff below. The candidate is already accepted and committed. Do not edit files, create commits, amend commits, push, create or modify remote artifacts, or change the worktree. Return a concise human-readable delivery summary. Report every unmet requirement exactly as listed; acceptance above 80 percent does not mean all requirements passed and must not be described as 100 percent.\n\nUnmet requirements: " + jsonText(delivery.UnmetRequirements) +
		"\nPlan: " + jsonText(step.task.Plan) +
		"\nReview: " + jsonText(step.task.Delivery.Adherence) +
		"\nVerification ID: " + delivery.VerificationID +
		"\nReviewed commit: " + delivery.ReviewedCommit +
		"\nDiff:\n" + delivery.Diff
	out, err := step.agent(step.ctx, step.project, step.task, provider.Input{Role: "finalizer", Purpose: "local_delivery_summary", CWD: delivery.Worktree, Prompt: prompt, ReadOnly: true})
	if err != nil {
		return true, err
	}
	if strings.TrimSpace(out.Text) == "" {
		return true, errors.New("finalizer returned an empty local delivery summary")
	}
	if _, err := promoteReviewedSnapshot(step.ctx, delivery.OriginalWorktree, delivery.Worktree, delivery.Branch, delivery.Base, delivery.ReviewedCommit); err != nil {
		return true, fmt.Errorf("finalizer changed the accepted candidate snapshot: %w", err)
	}
	delivery.Message = strings.TrimSpace(out.Text)
	delivery.Stage = "complete"
	delivery.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	step.task.LastAttemptID = out.AttemptID
	step.task.Stage = "complete"
	step.task.Error = ""
	step.task.Wait = nil
	step.task.Handoff.NextAction = "Review the local delivery branch and commit; decide whether to push it"
	notice := model.Notification{ID: store.ID(), TaskID: step.task.ID, Kind: "delivery", Message: "Local delivery ready at " + delivery.Branch + " (" + delivery.ReviewedCommit + ")"}
	err = step.Store.Write(step.ctx, []store.Change{
		{Kind: "tasks", ID: step.task.ID, Value: step.task, Expected: step.record.Version},
		{Kind: "inbox", ID: notice.ID, Value: notice},
	}, nil)
	return true, err
}
