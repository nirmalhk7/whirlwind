package workflow

import (
	"context"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/verification"
)

func runSnapshotCheckOwned(ctx context.Context, engine *Engine, scratch, snapshot string, check model.Check, current model.Project, owner runtimeCheckOwner) (verification.Result, execution.Profile, error) {
	profile, err := reviewerSnapshotProfile(scratch, snapshot, current)
	if err != nil {
		return verification.Result{}, execution.Profile{}, err
	}
	effective, err := reviewerCheckExecution(profile, check)
	if err != nil {
		return verification.Result{}, execution.Profile{}, err
	}
	result, err := engine.runOwnedCheck(ctx, snapshot, check, effective, owner)
	if err != nil {
		return verification.Result{}, execution.Profile{}, err
	}
	return result, effective, nil
}
