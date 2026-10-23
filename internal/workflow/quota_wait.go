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
		task, err := store.Decode[model.Task](record)
		if err != nil || task.Stage != "waiting_quota" || task.Wait == nil {
			continue
		}
		wait := task.Wait
		due, err := time.Parse(time.RFC3339Nano, wait.NextCheckAt)
		if err == nil && time.Now().Before(due) {
			continue
		}
		project, err := e.Project(ctx, task.ProjectID)
		if err != nil || project.Paused || e.settings(ctx).Stopped {
			continue
		}
		if task.PlanVersion != wait.PlanVersion || (wait.PolicyHash != "" && project.PolicyHash != wait.PolicyHash) {
			task.Stage = "paused"
			task.Error = "Waiting task intent or policy changed; review before resuming"
			task.Wait = nil
			_ = e.Store.Put(ctx, "tasks", task.ID, task, record.Version)
			continue
		}
		run := &agentRun{Engine: e, ctx: ctx, project: project, task: task}
		run.input.CWD = project.Path
		run.input.Role = wait.Role
		providerRecord, err := e.Store.Get(ctx, "providers", wait.Provider)
		if err != nil {
			continue
		}
		p, err := run.refreshBlockedQuota(providerRecord)
		if err != nil {
			continue
		}
		// Fresh metadata may be required even while the last summary was available.
		if reader, ok := e.Adapter(p).(quotaSnapshotReader); ok && p.Kind == "codex" {
			latest, getErr := e.Store.Get(ctx, "providers", p.ID)
			if getErr == nil {
				p, err = run.refreshSnapshot(latest, p, reader)
			}
		}
		if err != nil {
			continue
		}
		reserve, admissionErr := run.quotaAdmission(p)
		err = admissionErr
		if err == nil && wait.Kind == "capacity" {
			records, listErr := e.Store.List(ctx, "budget_reservations")
			if listErr != nil {
				continue
			}
			active := 0
			for _, r := range records {
				reservation, decodeErr := store.Decode[model.BudgetReservation](r)
				if decodeErr != nil {
					continue
				}
				if reservation.Pool == providerPool(p) && (reservation.Status == "active" || reservation.Status == "uncertain") {
					active++
				}
			}
			limit := reserve.MaxConcurrent
			if limit <= 0 {
				limit = 1
			}
			if active >= limit {
				err = &AdmissionWait{Kind: "capacity", Reason: wait.Reason, Provider: p.ID, Pool: wait.Pool, Role: wait.Role, NextCheck: time.Now().Add(time.Minute)}
			}
		}
		if err == nil {
			task.Stage = wait.ResumeStage
			task.Wait = nil
			task.Error = ""
		} else {
			var next *AdmissionWait
			if !errors.As(err, &next) {
				continue
			}
			wait.Reason = next.Reason
			wait.NextCheckAt = next.NextCheck.UTC().Format(time.RFC3339Nano)
			task.Error = next.Reason
		}
		_ = e.Store.Put(ctx, "tasks", task.ID, task, record.Version)
	}
}
