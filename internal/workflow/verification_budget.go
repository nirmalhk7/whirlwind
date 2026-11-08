package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type verificationBudgetRun struct {
	engine   *Engine
	ctx      context.Context
	checkCtx context.Context
	cancel   context.CancelFunc
	started  time.Time
	id       string
	project  model.Project
	task     model.Task
}

// verificationBudget charges deterministic check wall runtime once; reviewer-broker checks are already inside their AI attempt clock.
func (e *Engine) verificationBudget(ctx context.Context, p model.Project, t model.Task) (context.Context, func() error, error) {
	reservation, id, err := e.reserveVerificationBudget(ctx, p, t)
	if err != nil {
		return nil, nil, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(reservation.RuntimeSeconds)*time.Second)
	run := verificationBudgetRun{engine: e, ctx: ctx, checkCtx: checkCtx, cancel: cancel, started: time.Now(), id: id, project: p, task: t}
	finish := func() error { return run.finish() }
	return checkCtx, finish, nil
}

func (e *Engine) reserveVerificationBudget(ctx context.Context, p model.Project, t model.Task) (model.BudgetReservation, string, error) {
	run := &agentRun{Engine: e, ctx: ctx, project: p, task: t, input: provider.Input{Role: "review"}}
	policy, err := run.budgetPolicy()
	if err != nil {
		return model.BudgetReservation{}, "", err
	}
	id := store.ID()
	reservation := model.BudgetReservation{AttemptID: id, TaskID: t.ID, Owner: e.owner, Role: "verify", Kind: "verification", RuntimeSeconds: p.Policy.RunTimeoutSeconds}
	if err = e.Store.ReserveBudget(ctx, reservation, policy); err != nil {
		return model.BudgetReservation{}, "", err
	}
	record, err := e.Store.Get(ctx, "budget_reservations", id)
	if err != nil {
		return model.BudgetReservation{}, "", err
	}
	reservation, err = store.Decode[model.BudgetReservation](record)
	if err != nil {
		return model.BudgetReservation{}, "", err
	}
	return reservation, id, nil
}

func (run verificationBudgetRun) finish() error {
	elapsed := time.Since(run.started).Milliseconds()
	limited := errors.Is(run.checkCtx.Err(), context.DeadlineExceeded) && run.ctx.Err() == nil
	run.cancel()
	saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer saveCancel()
	entry := model.UsageEntry{Kind: "verification", AttemptID: run.id, TaskID: run.task.ID, ProjectID: run.project.ID, Role: "verify", ElapsedMS: elapsed}
	if err := run.engine.Store.SettleUsage(saveCtx, entry); err != nil {
		return fmt.Errorf("%w: %v", store.ErrPersistence, err)
	}
	if limited {
		return fmt.Errorf("%w: deterministic checks reached task runtime ceiling", store.ErrBudgetExceeded)
	}
	return nil
}
