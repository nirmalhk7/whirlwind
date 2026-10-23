package workflow

import (
	"context"
	"errors"
	"fmt"
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

func providerPool(p model.Provider) string {
	if p.QuotaPool != "" {
		return p.QuotaPool
	}
	return p.ID
}
func (a *agentRun) budgetPolicy() (model.BudgetPolicy, error) {
	var b *model.BudgetPolicy
	if a.input.Role == "ideate" {
		b = a.settings(a.ctx).Admission.DiscoveryBudget
		if b == nil {
			return model.BudgetPolicy{}, fmt.Errorf("%w: configure a separate finite discovery_budget", store.ErrBudgetExceeded)
		}
		if err := b.Validate(); err != nil {
			return *b, fmt.Errorf("%w: %v", store.ErrBudgetExceeded, err)
		}
		return *b, nil
	}
	if a.task.Budget != nil {
		b = a.task.Budget
	} else if a.project.Policy.Budget != nil {
		b = a.project.Policy.Budget
	} else {
		b = a.settings(a.ctx).Admission.DefaultBudget
	}
	if b == nil {
		return model.BudgetPolicy{}, fmt.Errorf("%w: configure finite task attempt and active-runtime ceilings", store.ErrBudgetExceeded)
	}
	if err := b.Validate(); err != nil {
		return *b, fmt.Errorf("%w: %v", store.ErrBudgetExceeded, err)
	}
	return *b, nil
}
func (a *agentRun) quotaAdmission(p model.Provider) (model.QuotaReserve, error) {
	if err := a.cooldown(p); err != nil {
		return model.QuotaReserve{}, err
	}
	pool := providerPool(p)
	settings := a.settings(a.ctx).Admission
	reserve, ok := settings.Pools[pool]
	if !ok {
		reserve, ok = settings.Pools["*"]
	}
	wait := &AdmissionWait{Kind: "quota", Provider: p.ID, Pool: pool, Role: a.input.Role, NextCheck: time.Now().Add(time.Minute)}
	if !ok || reserve.MaxAgeSeconds <= 0 {
		wait.Kind = "setup"
		wait.Reason = "Configure quota reserve and freshness for pool " + pool
		return reserve, wait
	}
	if reserve.MinRemainingPercent < 0 || reserve.MinRemainingPercent >= 100 {
		wait.Kind = "setup"
		wait.Reason = "Quota reserve must be at least 0 and below 100 percent"
		return reserve, wait
	}
	if p.Quota == "exhausted" {
		wait.Reason = "quota exhausted; waiting for quota in pool " + pool
		if reset, err := time.Parse(time.RFC3339, p.ResetAt); err == nil && reset.After(time.Now()) {
			wait.NextCheck = reset
		}
		return reserve, wait
	}
	// An explicitly configured zero reserve disables reserve protection for that pool.
	if reserve.MinRemainingPercent == 0 {
		return reserve, nil
	}
	snapshot := p.QuotaSnapshot
	if record, err := a.Store.Get(a.ctx, "quota_snapshots", pool); err == nil {
		current, decodeErr := store.Decode[model.QuotaSnapshot](record)
		if decodeErr != nil {
			return reserve, decodeErr
		}
		if snapshot == nil || current.ObservedAt > snapshot.ObservedAt {
			snapshot = &current
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return reserve, err
	}
	if snapshot == nil || snapshot.Pool != pool {
		wait.Reason = "Quota unknown for protected pool " + pool
		return reserve, wait
	}
	observed, err := time.Parse(time.RFC3339Nano, snapshot.ObservedAt)
	if err != nil || observed.After(time.Now().Add(time.Minute)) || time.Since(observed) > time.Duration(reserve.MaxAgeSeconds)*time.Second {
		wait.Reason = "Quota observation stale for protected pool " + pool
		return reserve, wait
	}
	if len(snapshot.Windows) == 0 {
		wait.Reason = "Quota unknown for protected pool " + pool
		return reserve, wait
	}
	for _, window := range snapshot.Windows {
		if window.UsedPercent == nil {
			wait.Reason = "Quota window utilization unknown for protected pool " + pool
			return reserve, wait
		}
		if 100-*window.UsedPercent <= reserve.MinRemainingPercent {
			wait.Reason = "Interactive quota reserve reached in pool " + pool
			if window.ResetsAt != nil {
				reset := time.Unix(*window.ResetsAt, 0)
				if reset.After(time.Now()) && reset.After(wait.NextCheck) {
					wait.NextCheck = reset
				}
			}
			return reserve, wait
		}
	}
	return reserve, nil
}
func (a *agentRun) reserveAttempt(chosen model.Provider, id string) (model.BudgetReservation, error) {
	if a.input.Role == "ideate" {
		a.mu.Lock()
		selectedActive := len(a.running) > 0
		a.mu.Unlock()
		if selectedActive {
			return model.BudgetReservation{}, &AdmissionWait{Kind: "capacity", Reason: "Discovery deferred while selected tasks are running", Provider: chosen.ID, Pool: providerPool(chosen), Role: a.input.Role, NextCheck: time.Now().Add(time.Minute)}
		}
	}
	b, err := a.budgetPolicy()
	if err != nil {
		return model.BudgetReservation{}, err
	}
	reserve, err := a.quotaAdmission(chosen)
	if err != nil {
		return model.BudgetReservation{}, err
	}
	concurrency := reserve.MaxConcurrent
	if concurrency <= 0 {
		concurrency = 1
	}
	r := model.BudgetReservation{RuntimeSeconds: a.project.Policy.RunTimeoutSeconds, AttemptID: id, TaskID: a.task.ID, Owner: a.owner, Provider: chosen.ID, Model: a.input.Model, Pool: providerPool(chosen), Role: a.input.Role, PoolConcurrency: concurrency}
	if err = a.Store.ReserveBudget(a.ctx, r, b); errors.Is(err, store.ErrPoolBusy) {
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
	record, err := a.Store.Get(ctx, "budget_reservations", a.id)
	if err != nil {
		return nil, nil, err
	}
	r, err := store.Decode[model.BudgetReservation](record)
	if err != nil {
		return nil, nil, err
	}
	run, clock := newRuntimeBudget(ctx, time.Duration(r.RuntimeSeconds)*time.Second)
	a.budgetClock = clock
	return run, clock.stop, nil
}
