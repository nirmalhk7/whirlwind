package workflow

import (
	"context"
	"errors"
	"log"
	"time"

	"whirlwind/internal/store"
)

func (step *taskStep) finishStep(failure error, onFailure func(*taskStep, error)) error {
	if onFailure == nil || !stepFailed(failure) {
		return failure
	}
	onFailure(step, failure)
	return nil
}

// Dispatch records failure while Step still owns its lease. Returning from the
// callback must not cause an unfenced second attempt after that lease releases.
func (e *Engine) failOwnedStep(step *taskStep, failure error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(step.ctx), 5*time.Second)
	defer cancel()
	if err := step.pauseOwnedFailure(ctx, failure); err != nil && !errors.Is(err, store.ErrLeased) && !errors.Is(err, store.ErrConflict) {
		log.Printf("persist owned plan recovery %s: %v", step.task.ID, err)
	}
}

func (step *taskStep) pauseOwnedFailure(ctx context.Context, failure error) error {
	staged, batch := store.Stage(ctx)
	if err := step.Store.AssertVersion(staged, "execution_plans", step.task.ID, step.record.Version); err != nil {
		return err
	}
	if err := step.pauseFailure(staged, step.task.ID, failure); err != nil {
		return err
	}
	return batch.Commit(ctx, step.Store, nil)
}
