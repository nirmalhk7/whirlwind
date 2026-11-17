package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func finalizationKey(plan model.ExecutionPlan) string {
	return fmt.Sprintf("%s:%d", plan.ID, plan.PlanVersion)
}
func finalizationIntent(plan model.ExecutionPlan) model.LocalFinalization {
	d := plan.Delivery
	return model.LocalFinalization{PlanID: plan.ID, ProjectID: plan.ProjectID, PlanVersion: plan.PlanVersion, DocumentHash: plan.PlanDocument.ApprovedHash, ConfigurationHash: verificationConfigurationHash(plan), ReviewedCommit: d.ReviewedCommit, Commit: d.ReviewedCommit, Tree: d.Tree, Base: d.Base, Branch: d.Branch, Worktree: d.Worktree, ReviewID: d.ReviewID, VerificationID: d.VerificationID, PolicyHash: d.PolicyHash}
}
func (a *agentAttempt) finalizeDelivery(ctx context.Context) (provider.FinalizationResult, error) {
	a.checkMu.Lock()
	defer a.checkMu.Unlock()
	if err := ctx.Err(); err != nil {
		return provider.FinalizationResult{}, err
	}
	if a.input.Role != "finalizer" || a.input.Purpose != "local_finalization" || !a.input.ReadOnly {
		return provider.FinalizationResult{}, errors.New("scoped finalization operation requires read-only Finalizer")
	}
	planRecord, plan, err := a.loadFinalizerPlan(ctx)
	if err != nil {
		return provider.FinalizationResult{}, err
	}
	if err := a.authorizeFinalizerPlan(ctx, planRecord, plan); err != nil {
		return provider.FinalizationResult{}, err
	}
	source, worktree, err := a.finalizerPaths(ctx, plan)
	if err != nil {
		return provider.FinalizationResult{}, err
	}
	intent, err := prepareFinalizationIntent(ctx, source, plan)
	if err != nil {
		return provider.FinalizationResult{}, err
	}
	key := finalizationKey(plan)
	if err := a.persistFinalizationIntent(ctx, planRecord, plan, key, intent); err != nil {
		return provider.FinalizationResult{}, err
	}
	complete, err := a.completedFinalization(ctx, source, key, intent)
	if err != nil {
		return provider.FinalizationResult{}, err
	}
	if complete {
		return finalizationResult(intent), nil
	}
	if err := a.publishFinalization(ctx, source, worktree, planRecord, key, intent); err != nil {
		return provider.FinalizationResult{}, err
	}
	return finalizationResult(intent), nil
}

func (a *agentAttempt) loadFinalizerPlan(ctx context.Context) (model.Record, model.ExecutionPlan, error) {
	planRecord, err := a.Store.Get(ctx, "execution_plans", a.task.ID)
	if err != nil {
		return model.Record{}, model.ExecutionPlan{}, err
	}
	plan, err := store.Decode[model.ExecutionPlan](planRecord)
	if err != nil {
		return model.Record{}, model.ExecutionPlan{}, err
	}
	if plan.IdeaID == "" || plan.ProjectID != a.project.ID || plan.PlanVersion != a.task.PlanVersion || plan.Stage != "submitting" || plan.Delivery == nil || plan.Delivery.Stage != "snapshot_ready" || plan.PlanDocument == nil || plan.PlanDocument.ApprovedHash == "" || a.task.PlanDocument == nil || plan.PlanDocument.ApprovedHash != a.task.PlanDocument.ApprovedHash {
		return model.Record{}, model.ExecutionPlan{}, store.ErrConflict
	}
	return planRecord, plan, nil
}

func (a *agentAttempt) authorizeFinalizerPlan(ctx context.Context, planRecord model.Record, plan model.ExecutionPlan) error {
	if err := a.withLiveFinalizerAttempt(ctx, func(model.Record, model.Attempt) error { return nil }); err != nil {
		return err
	}
	if err := a.ValidateLocalPlan(ctx, plan); err != nil {
		return err
	}
	if err := a.validateApprovalConfiguration(ctx, plan); err != nil {
		return err
	}
	project, err := a.trustedProject(ctx, plan.ProjectID)
	if err != nil {
		return err
	}
	step := taskStep{Engine: a.Engine, ctx: ctx, project: project, task: plan, record: planRecord}
	if _, accepted, err := step.acceptedDeliveryReport(); err != nil || !accepted {
		return errors.Join(errors.New("finalization operation requires accepted evidence"), err)
	}
	delivery := plan.Delivery
	if delivery.ReviewedCommit != plan.ReviewedCommit || delivery.Base != plan.Base || delivery.PlanVersion != plan.PlanVersion || delivery.ReviewID != plan.ReviewID || delivery.VerificationID != plan.VerificationID || delivery.PolicyHash != project.PolicyHash {
		return errors.New("saved delivery metadata differs from accepted evidence")
	}
	return nil
}

func (a *agentAttempt) finalizerPaths(ctx context.Context, plan model.ExecutionPlan) (string, string, error) {
	inputPath, err := submissionPath(a.input.CWD)
	if err != nil {
		return "", "", err
	}
	worktree, err := submissionPath(plan.Delivery.Worktree)
	if err != nil || worktree != inputPath {
		return "", "", errors.New("finalizer worktree identity mismatch")
	}
	source, err := submissionPath(plan.Delivery.OriginalWorktree)
	if err != nil {
		return "", "", err
	}
	if err := validateDetachedDeliveryWorktree(ctx, source, worktree, plan.Delivery.ReviewedCommit); err != nil {
		return "", "", err
	}
	return source, worktree, nil
}

func prepareFinalizationIntent(ctx context.Context, source string, plan model.ExecutionPlan) (model.LocalFinalization, error) {
	intent := finalizationIntent(plan)
	if err := validateFinalizationTree(ctx, source, intent); err != nil {
		return model.LocalFinalization{}, err
	}
	var err error
	intent.Commit, err = createFinalizationCommit(ctx, source, intent)
	if err != nil {
		return model.LocalFinalization{}, err
	}
	if err := validateFinalizationTree(ctx, source, intent); err != nil {
		return model.LocalFinalization{}, err
	}
	return intent, nil
}

func (a *agentAttempt) persistFinalizationIntent(ctx context.Context, planRecord model.Record, plan model.ExecutionPlan, key string, intent model.LocalFinalization) error {
	if record, err := a.Store.Get(ctx, "finalization_intents", key); err == nil {
		prior, err := store.Decode[model.LocalFinalization](record)
		if err != nil || !reflect.DeepEqual(prior, intent) {
			return store.ErrConflict
		}
	} else if errors.Is(err, store.ErrNotFound) {
		if err := a.withLiveFinalizerAttempt(ctx, func(attemptRecord model.Record, _ model.Attempt) error {
			return a.Store.Write(ctx, []store.Change{store.VersionPrecondition("execution_plans", plan.ID, planRecord.Version), store.VersionPrecondition("sessions", a.id, attemptRecord.Version), {Kind: "finalization_intents", ID: key, Value: intent}}, nil)
		}); err != nil {
			return err
		}
	} else {
		return err
	}
	return nil
}

func (a *agentAttempt) completedFinalization(ctx context.Context, source, key string, intent model.LocalFinalization) (bool, error) {
	if record, err := a.Store.Get(ctx, "finalizations", key); err == nil {
		prior, err := store.Decode[model.LocalFinalization](record)
		if err != nil || prior != intent {
			return false, store.ErrConflict
		}
		if err := validateFinalizationReference(ctx, source, intent); err != nil {
			return false, err
		}
		return true, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	return false, nil
}

func (a *agentAttempt) publishFinalization(ctx context.Context, source, worktree string, planRecord model.Record, key string, intent model.LocalFinalization) error {
	publishCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := a.withLiveFinalizerAttempt(publishCtx, func(attemptRecord model.Record, current model.Attempt) error {
		return a.Store.CommitLocalDelivery(publishCtx, planRecord, attemptRecord, store.LocalDeliveryOwner{Owner: a.owner, Generation: current.ExecutionGeneration}, key, intent, func() error {
			return publishFinalizationReference(publishCtx, source, worktree, intent)
		})
	})
	return err
}

func publishFinalizationReference(ctx context.Context, source, worktree string, intent model.LocalFinalization) error {
	if err := validateDetachedDeliveryWorktree(ctx, source, worktree, intent.ReviewedCommit); err != nil {
		return err
	}
	if err := validateFinalizationTree(ctx, source, intent); err != nil {
		return err
	}
	if err := validateSubmissionBranch(ctx, source, intent.Branch); err != nil {
		return err
	}
	return ensureDeliveryBranch(ctx, source, intent.Branch, intent.Commit)
}

func finalizationResult(intent model.LocalFinalization) provider.FinalizationResult {
	return provider.FinalizationResult{Branch: intent.Branch, Commit: intent.Commit, Tree: intent.Tree}
}
func validateFinalizationTree(ctx context.Context, source string, intent model.LocalFinalization) error {
	accepted, err := submissionGitRun(ctx, source, "rev-parse", intent.ReviewedCommit+"^{tree}")
	if err != nil || accepted != intent.Tree {
		return errors.Join(errors.New("saved delivery tree differs from accepted source tree"), err)
	}
	tree, err := submissionGitRun(ctx, source, "rev-parse", intent.Commit+"^{tree}")
	if err != nil || tree != intent.Tree {
		return errors.Join(errors.New("finalization changed accepted tree"), err)
	}
	return nil
}
func validateFinalizationReference(ctx context.Context, source string, intent model.LocalFinalization) error {
	commit, err := submissionGitRun(ctx, source, "show-ref", "--verify", "--hash", "refs/heads/"+intent.Branch)
	if err != nil || commit != intent.Commit {
		return errors.Join(errors.New("finalization delivery reference changed"), err)
	}
	return validateFinalizationTree(ctx, source, intent)
}
