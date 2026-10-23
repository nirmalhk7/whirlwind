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

// verificationBudget charges deterministic check wall runtime once; reviewer-broker checks are already inside their AI attempt clock.
func (e *Engine) verificationBudget(ctx context.Context, p model.Project, t model.Task) (context.Context, func() error, error) {
	run := &agentRun{Engine: e, ctx: ctx, project: p, task: t, input: provider.Input{Role: "review"}}
	policy, err := run.budgetPolicy()
	if err != nil {
		return nil, nil, err
	}
	id := store.ID()
	reservation := model.BudgetReservation{AttemptID: id, TaskID: t.ID, Owner: e.owner, Role: "verify", Kind: "verification", RuntimeSeconds: p.Policy.RunTimeoutSeconds}
	if err = e.Store.ReserveBudget(ctx, reservation, policy); err != nil {
		return nil, nil, err
	}
	record, err := e.Store.Get(ctx, "budget_reservations", id)
	if err != nil {
		return nil, nil, err
	}
	reservation, err = store.Decode[model.BudgetReservation](record)
	if err != nil {
		return nil, nil, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(reservation.RuntimeSeconds)*time.Second)
	started := time.Now()
	finish := func() error {
		elapsed := time.Since(started).Milliseconds()
		limited := errors.Is(checkCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		cancel()
		saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer saveCancel()
		entry := model.UsageEntry{Kind: "verification", AttemptID: id, TaskID: t.ID, ProjectID: p.ID, Role: "verify", ElapsedMS: elapsed}
		if err := e.Store.SettleUsage(saveCtx, entry); err != nil {
			return fmt.Errorf("%w: %v", store.ErrPersistence, err)
		}
		if limited {
			return fmt.Errorf("%w: deterministic checks reached task runtime ceiling", store.ErrBudgetExceeded)
		}
		return nil
	}
	return checkCtx, finish, nil
}
