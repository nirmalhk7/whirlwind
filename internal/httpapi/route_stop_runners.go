package httpapi

import (
	"errors"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

const globalStopReason = "Paused by global runner stop"

func (q apiRequest) stopRunners() error {
	if q.id != "stop" && q.id != "resume" {
		return &apiError{404, "route_not_found", "Unsupported route"}
	}
	settings, version, err := q.runnerSettings()
	if err != nil {
		return err
	}
	settings.Stopped = q.id == "stop"
	if err := q.Store.Put(q.r.Context(), "settings", "global", settings, version); err != nil {
		return err
	}
	if q.id == "resume" {
		return q.resumeRunners()
	}
	return q.pauseRunners()
}

func (q apiRequest) runnerSettings() (workflow.Settings, int, error) {
	settings := workflow.DefaultSettings()
	record, err := q.Store.Get(q.r.Context(), "settings", "global")
	if err == nil {
		settings, err = store.Decode[workflow.Settings](record)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return settings, 0, err
	}
	return settings, record.Version, nil
}

func (q apiRequest) pauseRunners() error {
	records, err := q.Store.List(q.r.Context(), "tasks")
	if err != nil {
		return err
	}
	paused := 0
	for _, record := range records {
		task, err := store.Decode[model.Task](record)
		if err != nil {
			return err
		}
		switch task.Stage {
		case "awaiting_selection", "paused", "complete", "pr_created":
			continue
		}
		task.ResumeStage = task.Stage
		task.Stage = "paused"
		task.Error = globalStopReason
		if err := q.Store.Put(q.r.Context(), "tasks", record.ID, task, record.Version); err != nil {
			return err
		}
		paused++
	}
	if err := q.Store.Audit(q.r.Context(), model.Audit{Source: "local_operator", Action: "stop_all_runners", Target: "runners"}); err != nil {
		return err
	}
	store.AfterCommit(q.r.Context(), q.Engine.StopAll)
	reply(q.w, 200, map[string]int{"paused": paused})
	return nil
}

// resumeRunners restores only tasks suspended by the global stop action.
func (q apiRequest) resumeRunners() error {
	records, err := q.Store.List(q.r.Context(), "tasks")
	if err != nil {
		return err
	}
	resumed := 0
	for _, record := range records {
		task, err := store.Decode[model.Task](record)
		if err != nil {
			return err
		}
		if task.Stage != "paused" || task.Error != globalStopReason {
			continue
		}
		if err := q.resumeTask(&task); err != nil {
			return err
		}
		if err := q.Store.Put(q.r.Context(), "tasks", record.ID, task, record.Version); err != nil {
			return err
		}
		resumed++
	}
	reply(q.w, 200, map[string]int{"resumed": resumed})
	return nil
}
