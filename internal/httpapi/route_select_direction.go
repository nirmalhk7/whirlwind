package httpapi

import (
	"errors"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const directionUnselectedReason = "Direction deselected"

func (q apiRequest) selectDirection() error {
	d, record, err := readCurrent[model.Direction](q, "directions")
	if err != nil {
		return err
	}
	return q.processDirectionSelection(d, record)
}

func (q apiRequest) processDirectionSelection(d model.Direction, record model.Record) error {
	taskID := "direction-" + d.ID
	task := model.Task{ID: taskID, ProjectID: d.ProjectID, Issue: model.Issue{Title: d.Title, Body: d.Description}, PlanVersion: 1, Stage: "planning", Handoff: model.Handoff{Version: 1, NextAction: "plan selected direction"}}
	project, err := q.Engine.Project(q.r.Context(), d.ProjectID)
	if err != nil {
		return err
	}
	if project.PolicyHash == "" {
		task.Stage = "paused"
		task.ResumeStage = "planning"
		task.Error = "Configure and commit project policy, then confirm it before resuming planning."
		task.Handoff.NextAction = "confirm project policy and resume planning"
	}
	if err := q.unselectGroupPeers(d); err != nil {
		return err
	}
	if err := q.saveSelectedDirectionTask(task); err != nil {
		return err
	}
	d.Selected = true
	if err := q.Store.Put(q.r.Context(), "directions", q.id, d, record.Version); err != nil {
		return err
	}
	saved, _ := q.Store.Get(q.r.Context(), "tasks", taskID)
	reply(q.w, 202, saved)
	return nil
}

func (q apiRequest) saveSelectedDirectionTask(task model.Task) error {
	taskID := task.ID
	existing, loadErr := q.Store.Get(q.r.Context(), "tasks", taskID)
	switch {
	case errors.Is(loadErr, store.ErrNotFound):
		return q.Store.Put(q.r.Context(), "tasks", taskID, task, 0)
	case loadErr != nil:
		return loadErr
	default:
		current, err := store.Decode[model.Task](existing)
		if err != nil {
			return err
		}
		if current.Stage == "paused" && current.Error == directionUnselectedReason {
			current.Stage = current.ResumeStage
			current.ResumeStage = ""
			current.Error = ""
			return q.Store.Put(q.r.Context(), "tasks", taskID, current, existing.Version)
		}
	}
	return nil
}

func (q apiRequest) deselectDirection() error {
	d, record, err := readCurrent[model.Direction](q, "directions")
	if err != nil {
		return err
	}
	return q.saveDeselectedDirection(d, record)
}

func (q apiRequest) saveDeselectedDirection(d model.Direction, record model.Record) error {
	expected, err := expected(q.r)
	if err != nil {
		return err
	}
	if expected != record.Version {
		return store.ErrConflict
	}
	d.Selected = false
	if err := q.Store.Put(q.r.Context(), "directions", q.id, d, record.Version); err != nil {
		return err
	}
	if err := q.pauseDirectionTask("direction-" + d.ID); err != nil {
		return err
	}
	reply(q.w, 200, d)
	return nil
}

func (q apiRequest) pauseDirectionTask(id string) error {
	record, err := q.Store.Get(q.r.Context(), "tasks", id)
	if err != nil {
		return directionTaskLoadError(err)
	}
	task, err := store.Decode[model.Task](record)
	if err != nil {
		return err
	}
	if task.Stage == "complete" || task.Stage == "pr_created" || (task.Stage == "paused" && task.Error != globalStopReason) {
		return nil
	}
	if task.Stage != "paused" {
		task.ResumeStage = task.Stage
	}
	task.Stage = "paused"
	task.Error = directionUnselectedReason
	if err := q.Store.Put(q.r.Context(), "tasks", id, task, record.Version); err != nil {
		return err
	}
	store.AfterCommit(q.r.Context(), func() { q.Engine.Stop(id) })
	return nil
}

func directionTaskLoadError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}
