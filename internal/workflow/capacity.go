package workflow

import (
	"context"
	"encoding/json"
	"time"
	"whirlwind/internal/model"
)

type Settings struct {
	Admission                  model.AdmissionSettings `json:"admission,omitempty"`
	Stopped                    bool                    `json:"stopped"`
	GlobalSlots                int                     `json:"global_slots"`
	ProjectSlots               int                     `json:"project_slots"`
	ReconciliationSeconds      int                     `json:"reconciliation_seconds"`
	ProviderStatusCheckSeconds int                     `json:"provider_status_check_seconds"`
	Ideation                   IdeationSchedule        `json:"ideation"`
}

// settings loads global worker settings over the default four global slots, two project slots, and five-minute reconciliation interval.
func (e *Engine) settings(ctx context.Context) Settings {
	v := DefaultSettings()
	if r, err := e.Store.Get(ctx, "settings", "global"); err == nil {
		_ = json.Unmarshal(r.Data, &v)
	}
	return v
}

// acquire waits for both global and per-project execution capacity, or returns on cancellation.
func (e *Engine) acquire(ctx context.Context, p model.Project) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		settings := e.settings(ctx)
		if settings.Stopped {
			return ErrRunnersStopped
		}
		global := settings.GlobalSlots
		project := settings.ProjectSlots
		if p.Policy.GlobalSlots > 0 && p.Policy.GlobalSlots < global {
			global = p.Policy.GlobalSlots
		}
		if p.Policy.ProjectSlots > 0 {
			project = p.Policy.ProjectSlots
		}
		e.mu.Lock()
		total := 0
		for _, n := range e.active {
			total += n
		}
		if total < global && e.active[p.ID] < project {
			e.active[p.ID]++
			e.mu.Unlock()
			return nil
		}
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// release returns one reserved execution slot to the project capacity pool.
func (e *Engine) release(project string) { e.mu.Lock(); e.active[project]--; e.mu.Unlock() }
