package workflow

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/taskqueue"
)

func (e *Engine) dispatchQueuedTasks(ctx context.Context, records []model.Record, counts map[string]int) {
	// Preserve the durable UI pause for older unselected work, without
	// creating a delivery or running any role on it.
	for _, record := range records {
		plan, err := store.Decode[model.ExecutionPlan](record)
		if err != nil || !runnableStage(plan.Stage) || plan.HasSelection() {
			continue
		}
		if project, err := e.Project(ctx, plan.ProjectID); err == nil && !project.Paused {
			_, _ = e.requireSelection(ctx, plan, record)
		}
	}
	var dispatchErr error
	roles := taskqueue.Roles()
	start := e.taskQueueCursor
	for offset := range roles {
		index := (start + offset) % len(roles)
		role := roles[index]
		if err := e.taskQueues.Push(ctx, role, taskqueue.Push{}); err != nil {
			dispatchErr = err
			break
		}
		if !e.queueHasCapacity(ctx) {
			continue
		}
		delivery, err := e.taskQueues.Pop(ctx, role)
		if err != nil {
			dispatchErr = err
			break
		}
		if delivery != nil && e.dispatchQueuedDelivery(ctx, role, *delivery, counts) {
			e.taskQueueCursor = (index + 1) % len(roles)
		}
	}
	e.logTaskQueueError(dispatchErr)
}

func (e *Engine) queueHasCapacity(ctx context.Context) bool {
	limit := e.settings(ctx).GlobalSlots
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.running) < limit
}

func (e *Engine) dispatchQueuedDelivery(ctx context.Context, role taskqueue.Role, d taskqueue.Delivery, counts map[string]int) bool {
	task, record, err := loadStepInput(ctx, e.Store, d.PlanID)
	if err == nil && deliveryMatches(record, task, d) && e.startDispatchedTask(ctx, task, counts, role, &d) {
		return true
	}
	_ = e.taskQueues.Settle(ctx, role, d.Receipt, "release")
	return false
}

func (e *Engine) consumeQueuedTask(ctx, failureContext context.Context, role taskqueue.Role, d taskqueue.Delivery) {
	// A delivery may have waited for local admission; confirm its receipt is
	// still live before acquiring the independent execution fence.
	if err := e.taskQueues.Settle(ctx, role, d.Receipt, "renew"); err != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	var renewal sync.WaitGroup
	renewal.Go(func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := e.taskQueues.Settle(ctx, role, d.Receipt, "renew"); err != nil {
					cancel()
					return
				}
			}
		}
	})
	if err := e.runDeliveredStep(ctx, d.PlanID, e.failOwnedStep, role, &d); stepFailed(err) {
		e.fail(failureContext, d.PlanID, err)
	}
	cancel()
	renewal.Wait()
	// The authoritative transaction removes the completed revision. If it
	// remained runnable, release for retry instead of acknowledging it away.
	settleCtx, finish := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finish()
	if err := e.taskQueues.Settle(settleCtx, role, d.Receipt, "ack"); errors.Is(err, taskqueue.ErrConflict) {
		_ = e.taskQueues.Settle(settleCtx, role, d.Receipt, "release")
	}
}

func deliveryMatches(r model.Record, task model.ExecutionPlan, d taskqueue.Delivery) bool {
	return r.ID == d.PlanID && r.Version == d.Version && task.Stage == d.Stage
}

func (e *Engine) logTaskQueueError(err error) {
	if err == nil {
		e.taskQueueError = ""
	} else if message := err.Error(); message != e.taskQueueError {
		e.taskQueueError = message
		log.Printf("task queue dispatch: %v; will retry without bypassing private queue API", err)
	}
}
