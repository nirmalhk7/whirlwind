package workflow

import (
	"context"
	"errors"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type AdmissionWait struct {
	Cause                              error
	Kind, Reason, Provider, Pool, Role string
	NextCheck                          time.Time
}

func (w *AdmissionWait) Error() string { return w.Reason }
func (w *AdmissionWait) Unwrap() error { return w.Cause }

func (a *agentRun) reserveAttempt(chosen model.Provider, id string) (model.BudgetReservation, error) {
	if err := a.checkDiscoveryCapacity(chosen); err != nil {
		return model.BudgetReservation{}, err
	}
	if a.timeLimitedPreparation() {
		return model.BudgetReservation{}, nil
	}
	b, err := a.budgetPolicy()
	if err != nil {
		return model.BudgetReservation{}, err
	}
	reserve, err := a.quotaAdmission(chosen)
	if err != nil {
		return model.BudgetReservation{}, err
	}
	return a.persistAttemptReservation(chosen, id, reserve, b)
}

func (a *agentRun) checkDiscoveryCapacity(chosen model.Provider) error {
	if a.input.Role != "ideate" {
		return nil
	}
	a.mu.Lock()
	selectedActive := len(a.running) > 0
	a.mu.Unlock()
	if selectedActive {
		return &AdmissionWait{Kind: "capacity", Reason: "Discovery deferred while selected tasks are running", Provider: chosen.ID, Pool: providerPool(chosen), Role: a.input.Role, NextCheck: time.Now().Add(time.Minute)}
	}
	return nil
}

func (a *agentRun) persistAttemptReservation(chosen model.Provider, id string, reserve model.QuotaReserve, b model.BudgetPolicy) (model.BudgetReservation, error) {
	concurrency := reserve.MaxConcurrent
	if concurrency <= 0 {
		concurrency = 1
	}
	r := model.BudgetReservation{RuntimeSeconds: a.project.Policy.RunTimeoutSeconds, AttemptID: id, TaskID: a.task.ID, Owner: a.owner, Provider: chosen.ID, Model: a.input.Model, Pool: providerPool(chosen), Role: a.input.Role, PoolConcurrency: concurrency}
	if err := a.Store.ReserveBudget(a.ctx, r, b); errors.Is(err, store.ErrPoolBusy) {
		return r, &AdmissionWait{Kind: "capacity", Reason: "Waiting for quota pool capacity", Provider: chosen.ID, Pool: r.Pool, Role: a.input.Role, NextCheck: time.Now().Add(2 * time.Second)}
	} else if err != nil {
		return r, err
	}
	record, err := a.Store.Get(a.ctx, "budget_reservations", id)
	if err != nil {
		return r, err
	}
	return store.Decode[model.BudgetReservation](record)
}

// activeBudgetContext limits the attempt to its persisted share of remaining active runtime.
func (a *agentAttempt) activeBudgetContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	deadline, err := attemptDeadline(a.state)
	if err != nil {
		return nil, nil, err
	}
	deadlineContext, deadlineCancel := context.WithDeadline(ctx, deadline)
	if a.timeLimitedPreparation() {
		return deadlineContext, deadlineCancel, nil
	}
	record, err := a.Store.Get(deadlineContext, "budget_reservations", a.id)
	if err != nil {
		deadlineCancel()
		return nil, nil, err
	}
	r, err := store.Decode[model.BudgetReservation](record)
	if err != nil {
		deadlineCancel()
		return nil, nil, err
	}
	allowance := time.Duration(r.RuntimeSeconds) * time.Second
	if allowance > attemptWallClockLimit {
		allowance = attemptWallClockLimit
	}
	if untilDeadline := time.Until(deadline); allowance > untilDeadline {
		allowance = untilDeadline
	}
	if allowance <= 0 {
		deadlineCancel()
		return nil, nil, context.DeadlineExceeded
	}
	run, clock := newRuntimeBudget(deadlineContext, allowance)
	a.budgetClock = clock
	return run, func() { clock.stop(); deadlineCancel() }, nil
}
