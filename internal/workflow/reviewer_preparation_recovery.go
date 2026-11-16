package workflow

import (
	"errors"
	"path/filepath"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// A successful ACP attempt and its captured output survive failure of the
// separate checklist checkpoint transaction. Reuse that evidence on restart.
func (step *taskStep) recoverReviewerPreparation(key string) error {
	if _, err := step.Store.Get(step.ctx, "review_preparations", key); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	r, err := step.Store.Get(step.ctx, "execution_pairs", key)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	pair, err := store.Decode[executionPair](r)
	if err != nil {
		return err
	}
	if pair.PlanID != step.task.ID || pair.PlanVersion != step.task.PlanVersion || pair.ConfigurationHash != step.task.Plan.Approval.ConfigurationHash {
		return store.ErrConflict
	}
	r, err = step.Store.Get(step.ctx, "sessions", pair.PreparationRunID)
	if err != nil {
		return err
	}
	a, err := store.Decode[model.Attempt](r)
	if err != nil {
		return err
	}
	if !a.Finished || a.Status != "complete" {
		return nil
	}
	return step.restoreCompletedReviewerPreparation(key, pair, a)
}

func (step *taskStep) restoreCompletedReviewerPreparation(key string, pair executionPair, a model.Attempt) error {
	if a.Success == nil || !*a.Success || a.Task != pair.PlanID || a.PlanVersion != pair.PlanVersion || a.ProjectID != step.project.ID || a.Owner != pair.Owner || a.Role != "review" || a.Purpose != "review_preparation" {
		return store.ErrConflict
	}
	step.restoreWorkspace(step.ctx, &step.task)
	if step.task.Base != pair.Base || step.task.Worktree == "" {
		return store.ErrConflict
	}
	source, err := submissionPath(step.task.Worktree)
	if err != nil {
		return err
	}
	if err := validateReviewerSnapshot(step.ctx, step.project.Path, filepath.Join(filepath.Dir(source), "review-preparation"), pair.Base); err != nil {
		return err
	}
	artifact, output, err := step.Store.Artifact(step.ctx, a.OutputArtifact)
	if err != nil {
		return err
	}
	if artifact.Truncated {
		return errors.New("completed Reviewer preparation output is truncated")
	}
	checklist, err := parsePreparation(string(output), step.task.Plan.Requirements)
	if err != nil {
		return err
	}
	prepared := reviewerPreparation{PlanID: pair.PlanID, PlanVersion: pair.PlanVersion, DocumentHash: step.task.PlanDocument.ApprovedHash, Base: pair.Base, AttemptID: a.AttemptID, Checklist: checklist}
	return step.Store.Write(step.ctx, []store.Change{store.VersionPrecondition("execution_plans", step.task.ID, step.record.Version), {Kind: "review_preparations", ID: key, Value: prepared}}, nil)
}
