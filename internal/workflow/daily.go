package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// Daily runs ideation for the latest due project/day, skipping completed schedules and delaying failed schedules until retry time.
func (e *Engine) Daily(ctx context.Context) {
	settings := e.settings(ctx)
	if settings.Stopped {
		return
	}
	schedule := settings.Ideation
	if !schedule.Enabled {
		return
	}
	day, err := schedule.DueDay(time.Now())
	if err != nil {
		return
	}
	projects, err := e.Store.List(ctx, "projects")
	if err != nil {
		return
	}
	for _, r := range projects {
		p, err := store.Decode[model.Project](r)
		if err != nil || p.Paused {
			continue
		}
		if err := e.runDailyProject(ctx, p, day); err != nil {
			continue
		}
	}
}

func (e *Engine) runDailyProject(ctx context.Context, p model.Project, day string) error {
	ctx, release, err := e.projectRunnerContext(ctx, p.ID)
	if errors.Is(err, ErrProjectPaused) {
		return nil
	}
	if err != nil {
		return err
	}
	defer release()
	if err = ctx.Err(); err != nil {
		return err
	}
	key := p.ID + ":" + day
	leaseKey := "daily:" + p.ID
	lease, err := e.Store.Claim(ctx, leaseKey, e.owner, time.Hour)
	if err != nil {
		return err
	}
	defer func() { _ = e.Store.Release(context.Background(), leaseKey, e.owner, lease.Fence) }()
	return e.runScheduledIdeation(ctx, p, day, key)
}

func (e *Engine) runScheduledIdeation(ctx context.Context, p model.Project, day, key string) error {
	blocked, recovered, err := e.checkSetupBlock(ctx, p)
	if err != nil || blocked {
		return err
	}
	schedule, load := e.Store.Get(ctx, "schedules", key)
	if load != nil && !errors.Is(load, store.ErrNotFound) {
		return load
	}
	if load == nil && !scheduledRetryAllowed(schedule.Data, recovered) {
		return nil
	}
	return e.runDailyAttempt(ctx, p, day, key)
}

func scheduledRetryAllowed(data []byte, recovered bool) bool {
	var status struct{ Stage string }
	if json.Unmarshal(data, &status) != nil {
		return false
	}
	return status.Stage == "failed" && (recovered || scheduleRetryDue(data, time.Now()))
}

func (e *Engine) runDailyAttempt(ctx context.Context, p model.Project, day, key string) error {
	if err := e.Ideate(ctx, p, day); err != nil {
		if errors.Is(err, ErrProjectPaused) || errors.Is(err, context.Canceled) {
			return nil
		}
		var setup *setupFailure
		if errors.As(err, &setup) {
			if saveErr := e.saveSetupBlock(ctx, p, setup); saveErr != nil {
				return saveErr
			}
		}
		previous, _ := e.Store.Get(ctx, "schedules", key)
		if saveErr := e.Store.Put(ctx, "schedules", key, map[string]any{"day": day, "project": p.ID, "stage": "failed", "retry_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339), "error": err.Error()}, previous.Version); saveErr != nil {
			return saveErr
		}
		return e.Store.Notify(ctx, "", "ideation", connectionFailureMessage(p, err))
	}
	return nil
}

func scheduleRetryDue(data []byte, now time.Time) bool {
	var status struct {
		Stage   string `json:"stage"`
		RetryAt string `json:"retry_at"`
	}
	_ = json.Unmarshal(data, &status)
	if status.Stage != "failed" {
		return false
	}
	retry, _ := time.Parse(time.RFC3339, status.RetryAt)
	return !now.Before(retry)
}
