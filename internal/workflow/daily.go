package workflow

import (
	"context"
	"encoding/json"
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
		p, _ := store.Decode[model.Project](r)
		if err := e.runDailyProject(ctx, p, day); err != nil {
			continue
		}
	}
}

func (e *Engine) runDailyProject(ctx context.Context, p model.Project, day string) error {
	key := p.ID + ":" + day
	schedule, load := e.Store.Get(ctx, "schedules", key)
	if load == nil && !scheduleRetryDue(schedule.Data, time.Now()) {
		return nil
	}
	lease, err := e.Store.Claim(ctx, ideationPrefix+key, e.owner, time.Hour)
	if err != nil {
		return err
	}
	defer func() { _ = e.Store.Release(context.Background(), ideationPrefix+key, e.owner, lease.Fence) }()
	if err := e.Ideate(ctx, p, day); err != nil {
		previous, _ := e.Store.Get(ctx, "schedules", key)
		_ = e.Store.Put(ctx, "schedules", key, map[string]any{"day": day, "project": p.ID, "stage": "failed", "retry_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339), "error": err.Error()}, previous.Version)
		_ = e.Store.Notify(ctx, "", "ideation", p.Name+": "+err.Error())
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
