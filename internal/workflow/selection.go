package workflow

import (
	"context"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// requireSelection holds old or imported tasks before any agent or publication runs.
func (e *Engine) requireSelection(ctx context.Context, task model.Task, record model.Record) (bool, error) {
	if task.HasSelection() || task.Stage == "complete" || task.Stage == "pr_created" {
		return true, nil
	}
	if task.Stage == "awaiting_selection" {
		return false, nil
	}
	task.ResumeStage = task.Stage
	task.Stage = "awaiting_selection"
	task.Error = "Select or explicitly adopt this task before execution"
	task.Handoff.NextAction = "select or adopt task objective"
	candidate := model.Direction{ID: "adopt-" + task.ID, TaskID: task.ID, ProjectID: task.ProjectID, Title: task.Issue.Title, Description: task.Issue.Body, Status: "proposed", Scope: []string{task.Issue.Title}}
	changes := []store.Change{{Kind: "tasks", ID: task.ID, Value: task, Expected: record.Version}}
	if _, err := e.Store.Get(ctx, "directions", candidate.ID); err == store.ErrNotFound {
		changes = append(changes, store.Change{Kind: "directions", ID: candidate.ID, Value: candidate})
	} else if err != nil {
		return false, err
	}
	return false, e.Store.Write(ctx, changes, nil)
}
