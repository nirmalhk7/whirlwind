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
	schedule := e.settings(ctx).Ideation
	if !schedule.Enabled {
		return
	}
	day, err := schedule.DueDay(time.Now())
	if err != nil {
		return
	}
	projects, _ := e.Store.List(ctx, "projects")
	for _, r := range projects {
		p, _ := store.Decode[model.Project](r)
		key := p.ID + ":" + day
		schedule, load := e.Store.Get(ctx, "schedules", key)
		if load == nil {
			var status struct {
				Stage   string `json:"stage"`
				RetryAt string `json:"retry_at"`
			}
			_ = json.Unmarshal(schedule.Data, &status)
			if status.Stage != "failed" {
				continue
			}
			retry, _ := time.Parse(time.RFC3339, status.RetryAt)
			if time.Now().Before(retry) {
				continue
			}
		}
		lease, err := e.Store.Claim(ctx, ideationPrefix+key, e.owner, time.Hour)
		if err != nil {
			continue
		}
		err = e.Ideate(ctx, p, day)
		if err != nil {
			previous, _ := e.Store.Get(ctx, "schedules", key)
			_ = e.Store.Put(ctx, "schedules", key, map[string]any{"day": day, "project": p.ID, "stage": "failed", "retry_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339), "error": err.Error()}, previous.Version)
			_ = e.Store.Notify(ctx, "", "ideation", p.Name+": "+err.Error())
		}
		_ = e.Store.Release(context.Background(), ideationPrefix+key, e.owner, lease.Fence)
	}
}
