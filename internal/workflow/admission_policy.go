package workflow

import (
	"errors"
	"fmt"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func providerPool(p model.Provider) string {
	if p.QuotaPool != "" {
		return p.QuotaPool
	}
	return p.ID
}
func (a *agentRun) budgetPolicy() (model.BudgetPolicy, error) {
	if a.input.Role == "ideate" {
		return a.discoveryBudgetPolicy()
	}
	return a.taskBudgetPolicy()
}

func (a *agentRun) discoveryBudgetPolicy() (model.BudgetPolicy, error) {
	b := a.settings(a.ctx).Admission.DiscoveryBudget
	if b == nil {
		return model.BudgetPolicy{}, fmt.Errorf("%w: configure a separate finite discovery_budget", store.ErrBudgetExceeded)
	}
	if err := b.Validate(); err != nil {
		return *b, fmt.Errorf("%w: %v", store.ErrBudgetExceeded, err)
	}
	return *b, nil
}

func (a *agentRun) taskBudgetPolicy() (model.BudgetPolicy, error) {
	var b *model.BudgetPolicy
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
	return a.quotaAdmissionWithoutCooldown(p)
}

func (a *agentRun) quotaAdmissionWithoutCooldown(p model.Provider) (model.QuotaReserve, error) {
	if a.timeLimitedPreparation() {
		return model.QuotaReserve{}, nil
	}
	pool := providerPool(p)
	settings := a.settings(a.ctx).Admission
	wait := &AdmissionWait{Kind: "quota", Provider: p.ID, Pool: pool, Role: a.input.Role, NextCheck: time.Now().Add(time.Minute)}
	return a.checkQuotaAdmission(p, pool, settings, wait)
}

func (a *agentRun) checkQuotaAdmission(p model.Provider, pool string, settings model.AdmissionSettings, wait *AdmissionWait) (model.QuotaReserve, error) {
	reserve, err := configuredQuotaReserve(settings, pool, wait)
	if err != nil {
		return reserve, err
	}
	if p.Quota == "exhausted" {
		return reserve, exhaustedQuotaWait(p, wait)
	}
	// An explicitly configured zero reserve disables reserve protection for that pool.
	if reserve.MinRemainingPercent == 0 {
		return reserve, nil
	}
	return a.checkProtectedQuota(pool, reserve, wait, p.QuotaSnapshot)
}

func (a *agentRun) checkProtectedQuota(pool string, reserve model.QuotaReserve, wait *AdmissionWait, snapshot *model.QuotaSnapshot) (model.QuotaReserve, error) {
	snapshot, err := a.latestQuotaSnapshot(pool, snapshot)
	if err != nil {
		return reserve, err
	}
	if err := validateQuotaObservation(snapshot, pool, reserve, wait); err != nil {
		return reserve, err
	}
	return reserve, validateQuotaWindows(snapshot.Windows, pool, reserve, wait)
}

func configuredQuotaReserve(settings model.AdmissionSettings, pool string, wait *AdmissionWait) (model.QuotaReserve, error) {
	reserve, ok := settings.Pools[pool]
	if !ok {
		reserve, ok = settings.Pools["*"]
	}
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
	return reserve, nil
}

func exhaustedQuotaWait(provider model.Provider, wait *AdmissionWait) error {
	wait.Reason = "quota exhausted; waiting for quota in pool " + wait.Pool
	if reset, err := time.Parse(time.RFC3339, provider.ResetAt); err == nil && reset.After(time.Now()) {
		wait.NextCheck = reset
	}
	return wait
}

func (a *agentRun) latestQuotaSnapshot(pool string, snapshot *model.QuotaSnapshot) (*model.QuotaSnapshot, error) {
	record, err := a.Store.Get(a.ctx, "quota_snapshots", pool)
	if errors.Is(err, store.ErrNotFound) {
		return snapshot, nil
	}
	if err != nil {
		return nil, err
	}
	current, err := store.Decode[model.QuotaSnapshot](record)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || current.ObservedAt > snapshot.ObservedAt {
		snapshot = &current
	}
	return snapshot, nil
}

func validateQuotaObservation(snapshot *model.QuotaSnapshot, pool string, reserve model.QuotaReserve, wait *AdmissionWait) error {
	if snapshot == nil || snapshot.Pool != pool {
		wait.Reason = "Quota unknown for protected pool " + pool
		return wait
	}
	observed, err := time.Parse(time.RFC3339Nano, snapshot.ObservedAt)
	if err != nil || observed.After(time.Now().Add(time.Minute)) || time.Since(observed) > time.Duration(reserve.MaxAgeSeconds)*time.Second {
		wait.Reason = "Quota observation stale for protected pool " + pool
		return wait
	}
	if len(snapshot.Windows) == 0 {
		wait.Reason = "Quota unknown for protected pool " + pool
		return wait
	}
	return nil
}

func validateQuotaWindows(windows []model.QuotaWindow, pool string, reserve model.QuotaReserve, wait *AdmissionWait) error {
	for _, window := range windows {
		if window.UsedPercent == nil {
			wait.Reason = "Quota window utilization unknown for protected pool " + pool
			return wait
		}
		if 100-*window.UsedPercent <= reserve.MinRemainingPercent {
			wait.Reason = "Interactive quota reserve reached in pool " + pool
			if window.ResetsAt != nil {
				reset := time.Unix(*window.ResetsAt, 0)
				if reset.After(time.Now()) && reset.After(wait.NextCheck) {
					wait.NextCheck = reset
				}
			}
			return wait
		}
	}
	return nil
}
