package workflow

import (
	"context"
	"errors"
	"strings"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (step *taskStep) finishScopedLocalDelivery() (bool, error) {
	delivery := step.task.Delivery
	if err := prepareScopedDeliveryWorktree(step.ctx, delivery); err != nil {
		return true, err
	}
	prompt := "Inspect the exact accepted code version in this read-only worktree. Invoke finalize_delivery with an empty object to publish its local delivery reference. Only that trusted operation may finalize; do not edit files or run Git writes. No remote operations. Then summarize delivery and retain every unmet requirement.\nUnmet requirements: " + jsonText(delivery.UnmetRequirements) + "\nPlan: " + jsonText(step.task.Plan) + "\nReview: " + jsonText(delivery.Adherence) + "\nVerification ID: " + delivery.VerificationID + "\nAccepted commit: " + delivery.ReviewedCommit + "\nDiff:\n" + delivery.Diff
	out, err := step.agent(step.ctx, step.project, step.task, provider.Input{Role: "finalizer", Purpose: "local_finalization", CWD: delivery.Worktree, Prompt: prompt, ReadOnly: true})
	if err != nil {
		return true, err
	}
	record, err := step.Store.Get(step.ctx, "finalizations", finalizationKey(step.task))
	if err != nil {
		return true, errors.Join(errors.New("missing operation: Finalizer must invoke the scoped finalization operation before completing delivery"), err)
	}
	receipt, err := store.Decode[model.LocalFinalization](record)
	expected := finalizationIntent(step.task)
	expected.Commit = receipt.Commit
	if err != nil || receipt.Commit == "" || receipt != expected {
		return true, errors.New("scoped finalization operation receipt mismatch")
	}
	if strings.TrimSpace(out.Text) == "" {
		return true, errors.New("missing summary: Finalizer returned empty delivery summary")
	}
	if err := prepareScopedDeliveryWorktree(step.ctx, delivery); err != nil {
		return true, err
	}
	if err := validateFinalizationReference(step.ctx, delivery.OriginalWorktree, receipt); err != nil {
		return true, err
	}
	delivery.DeliveryCommit, delivery.Message, delivery.Stage, delivery.FinishedAt = receipt.Commit, strings.TrimSpace(out.Text), "complete", time.Now().UTC().Format(time.RFC3339Nano)
	step.task.LastAttemptID, step.task.Stage, step.task.Error = out.AttemptID, "complete", ""
	step.task.Wait = nil
	step.task.Handoff.NextAction = "Inspect the local delivery branch, commit, diff and verification evidence"
	notice := model.Notification{ID: store.ID(), TaskID: step.task.ID, Kind: "delivery", Message: "Local delivery ready at " + receipt.Branch + " (" + receipt.Commit + ")"}
	return true, step.Store.Write(step.ctx, []store.Change{{Kind: "execution_plans", ID: step.task.ID, Value: step.task, Expected: step.record.Version}, {Kind: "inbox", ID: notice.ID, Value: notice}}, nil)
}

func prepareScopedDeliveryWorktree(ctx context.Context, delivery *model.LocalDelivery) error {
	source, err := submissionPath(delivery.OriginalWorktree)
	if err != nil {
		return err
	}
	target, err := submissionPath(delivery.Worktree)
	if err != nil {
		return err
	}
	if source == target {
		return errors.New("local delivery requires a distinct worktree")
	}
	if err := ensureDetachedDeliveryWorktree(ctx, source, target, delivery.ReviewedCommit); err != nil {
		return err
	}
	return validateSubmissionTree(ctx, source, target, delivery.ReviewedCommit)
}
