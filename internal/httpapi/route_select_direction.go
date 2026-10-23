package httpapi

import (
	"errors"
	"net/url"
	"strings"
	"time"
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
	if d.Status == "dismissed" || d.Status == "deferred" {
		return &apiError{409, "candidate_inactive", "Reopen this candidate before selection"}
	}
	taskID := "direction-" + d.ID
	if d.TaskID != "" {
		taskID = d.TaskID
	}
	if r, err := q.Store.Get(q.r.Context(), "tasks", taskID); err == nil {
		if t, err := store.Decode[model.Task](r); err == nil && t.Cancelled() {
			taskID = "direction-" + d.ID + "-" + store.ID()
		}
	}
	task := model.Task{Mode: q.Engine.WorkflowConfiguration(q.r.Context()).ActiveMode, ID: taskID, ProjectID: d.ProjectID, Issue: model.Issue{Title: d.Title, Body: d.Description}, PlanVersion: 1, Stage: "planning", Handoff: model.Handoff{Version: 1, NextAction: "plan selected direction"}}
	for _, evidence := range d.Evidence {
		if evidence.Kind != "integration" {
			continue
		}
		host := ""
		if u, err := url.Parse(evidence.URL); err == nil {
			host = u.Hostname()
		}
		task.Sources = append(task.Sources, model.SourceRef{Provider: evidence.Provider, Host: host, Connection: evidence.Connection, Repository: d.ProjectID, ID: strings.TrimPrefix(evidence.Ref, evidence.Connection+":"), URL: evidence.URL, Revision: evidence.Revision})
	}
	if d.SourceIssue != nil {
		task.Issue = *d.SourceIssue
	}
	project, err := q.Engine.Project(q.r.Context(), d.ProjectID)
	if err != nil {
		return err
	}
	scope := d.Scope
	if len(scope) == 0 {
		scope = []string{d.Description}
	}
	task.Selection = &model.SelectionAuthorization{CandidateID: d.ID, CandidateVersion: record.Version, Objective: d.Title, Scope: scope, PolicyHash: project.PolicyHash, SourceFingerprint: d.Fingerprint, SelectedAt: time.Now().UTC().Format(time.RFC3339Nano)}
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
	d.Status = "selected"
	d.TaskID = taskID
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
		if current.Stage == "awaiting_selection" {
			current.Selection = task.Selection
			current.Stage = "planning"
			if current.ResumeStage != "" && current.ResumeStage != "awaiting_selection" && current.ResumeStage != "paused" {
				current.Stage = current.ResumeStage
			}
			current.ResumeStage = ""
			current.Error = ""
			if task.Stage == "paused" {
				current.Stage = task.Stage
				current.ResumeStage = task.ResumeStage
				current.Error = task.Error
			}
			return q.Store.Put(q.r.Context(), "tasks", taskID, current, existing.Version)
		}
		if current.Stage == "paused" && current.Error == directionUnselectedReason {
			current.Selection = task.Selection
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
	d.Status = "proposed"
	if err := q.Store.Put(q.r.Context(), "directions", q.id, d, record.Version); err != nil {
		return err
	}
	if err := q.pauseDirectionTask(directionTask(d)); err != nil {
		return err
	}
	reply(q.w, 200, d)
	return nil
}

func directionTask(d model.Direction) string {
	if d.TaskID != "" {
		return d.TaskID
	}
	return "direction-" + d.ID
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
	if task.Cancelled() || task.Stage == "complete" || task.Stage == "pr_created" || (task.Stage == "paused" && task.Error != globalStopReason) {
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
