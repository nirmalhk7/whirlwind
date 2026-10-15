package httpapi

import (
	"errors"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (q apiRequest) selectDirection() error {
	d, record, e := readCurrent[model.Direction](q, "directions")
	if e != nil {
		return e
	}
	taskID := "direction-" + d.ID
	task := model.Task{ID: taskID, ProjectID: d.ProjectID, Issue: model.Issue{Title: d.Title, Body: d.Description}, PlanVersion: 1, Stage: "planning", Handoff: model.Handoff{Version: 1, NextAction: "plan selected direction"}}
	project, e := q.Engine.Project(q.r.Context(), d.ProjectID)
	if e != nil {
		return e
	}
	if project.PolicyHash == "" {
		task.Stage = "paused"
		task.ResumeStage = "planning"
		task.Error = "Configure and commit project policy, then confirm it before resuming planning."
		task.Handoff.NextAction = "confirm project policy and resume planning"
	}
	if e = q.Store.Put(q.r.Context(), "tasks", taskID, task, 0); e != nil && !errors.Is(e, store.ErrConflict) {
		return e
	}
	d.Selected = true
	if e = q.Store.Put(q.r.Context(), "directions", q.id, d, record.Version); e != nil {
		return e
	}
	saved, _ := q.Store.Get(q.r.Context(), "tasks", taskID)
	reply(q.w, 202, saved)
	return nil
}
