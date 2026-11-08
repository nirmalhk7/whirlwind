package workflow

import (
	"context"
	"errors"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// refreshQuotaWaits performs metadata-only wake checks. Optimistic task versions fence a concurrent pause.
func (e *Engine) refreshQuotaWaits(ctx context.Context) {
	records, err := e.Store.List(ctx, "tasks")
	if err != nil {
		return
	}
	for _, record := range records {
		e.refreshQuotaWait(ctx, record)
	}
}

func (e *Engine) refreshQuotaWait(ctx context.Context, record model.Record) {
	task, err := store.Decode[model.Task](record)
	if err != nil || task.Stage != "waiting_quota" || task.Wait == nil || !quotaWaitIsDue(task.Wait.NextCheckAt) {
		return
	}
	wait := task.Wait
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil || project.Paused || e.settings(ctx).Stopped {
		return
	}
	if task.PlanVersion != wait.PlanVersion || (wait.PolicyHash != "" && project.PolicyHash != wait.PolicyHash) {
		pauseChangedQuotaWait(ctx, e.Store, record, &task)
		return
	}
	run := &agentRun{Engine: e, ctx: ctx, project: project, task: task}
	run.input.CWD = project.Path
	run.input.Role = wait.Role
	p, err := e.refreshWaitingProvider(ctx, run, wait.Provider)
	if err != nil {
		return
	}
	reserve, err := run.quotaAdmission(p)
	if err == nil && wait.Kind == "capacity" {
		err = e.capacityWaitError(ctx, p, reserve, wait)
	}
	if updateQuotaWaitResult(&task, err, wait) {
		_ = e.Store.Put(ctx, "tasks", task.ID, task, record.Version)
	}
}

func quotaWaitIsDue(nextCheck string) bool {
	due, err := time.Parse(time.RFC3339Nano, nextCheck)
	return err != nil || !time.Now().Before(due)
}

func pauseChangedQuotaWait(ctx context.Context, s *store.Store, record model.Record, task *model.Task) {
	task.Stage = "paused"
	task.Error = "Waiting task intent or policy changed; review before resuming"
	task.Wait = nil
	_ = s.Put(ctx, "tasks", task.ID, *task, record.Version)
}

func (e *Engine) refreshWaitingProvider(ctx context.Context, run *agentRun, providerID string) (model.Provider, error) {
	record, err := e.Store.Get(ctx, "providers", providerID)
	if err != nil {
		return model.Provider{}, err
	}
	provider, err := run.refreshBlockedQuota(record)
	if err != nil {
		return model.Provider{}, err
	}
	if reader, ok := e.Adapter(provider).(quotaSnapshotReader); ok && provider.Kind == "codex" {
		latest, getErr := e.Store.Get(ctx, "providers", provider.ID)
		if getErr == nil {
			provider, err = run.refreshSnapshot(latest, provider, reader)
		}
	}
	return provider, err
}

func (e *Engine) capacityWaitError(ctx context.Context, provider model.Provider, reserve model.QuotaReserve, wait *model.TaskWait) error {
	records, err := e.Store.List(ctx, "budget_reservations")
	if err != nil {
		return err
	}
	active := activeReservations(records, providerPool(provider))
	limit := reserve.MaxConcurrent
	if limit <= 0 {
		limit = 1
	}
	if active < limit {
		return nil
	}
	return &AdmissionWait{Kind: "capacity", Reason: wait.Reason, Provider: provider.ID, Pool: wait.Pool, Role: wait.Role, NextCheck: time.Now().Add(time.Minute)}
}

func activeReservations(records []model.Record, pool string) int {
	active := 0
	for _, record := range records {
		reservation, err := store.Decode[model.BudgetReservation](record)
		if err != nil {
			continue
		}
		if reservation.Pool == pool && (reservation.Status == "active" || reservation.Status == "uncertain") {
			active++
		}
	}
	return active
}

func updateQuotaWaitResult(task *model.Task, err error, wait *model.TaskWait) bool {
	if err == nil {
		task.Stage = wait.ResumeStage
		task.Wait = nil
		task.Error = ""
		return true
	}
	var next *AdmissionWait
	if !errors.As(err, &next) {
		return false
	}
	wait.Reason = next.Reason
	wait.NextCheckAt = next.NextCheck.UTC().Format(time.RFC3339Nano)
	task.Error = next.Reason
	return true
}
