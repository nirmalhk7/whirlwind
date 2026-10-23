package workflow

import (
	"context"
	"errors"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// dispatch counts active tasks once, then starts eligible work within each project's capacity.
func (e *Engine) dispatch(ctx context.Context) {
	if e.settings(ctx).Stopped {
		return
	}
	records, err := e.Store.List(ctx, "tasks")
	if err != nil {
		return
	}
	counts := e.runningCounts(records)
	for _, record := range records {
		task, err := store.Decode[model.Task](record)
		if err == nil && runnableStage(task.Stage) {
			e.dispatchTask(ctx, task, counts)
		}
	}
}
func runnableStage(stage string) bool {
	switch stage {
	case "plan_approval", "cancelled", "awaiting_selection", "paused", "questions", "approval", "pr_created", "complete":
		return false
	}
	return true
}
func (e *Engine) runningCounts(records []model.Record) map[string]int {
	counts := map[string]int{}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, record := range records {
		if _, running := e.running[record.ID]; running {
			task, _ := store.Decode[model.Task](record)
			counts[task.ProjectID]++
		}
	}
	return counts
}
func (e *Engine) dispatchTask(ctx context.Context, task model.Task, counts map[string]int) {
	record, err := e.Store.Get(ctx, "projects", task.ProjectID)
	if err != nil {
		return
	}
	project, _ := store.Decode[model.Project](record)
	if project.Paused {
		return
	}
	limit := project.Policy.ProjectSlots
	if limit <= 0 {
		limit = e.settings(ctx).ProjectSlots
	}
	if counts[project.ID] >= limit {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, busy := e.running[task.ID]; busy {
		return
	}
	run, cancel := context.WithCancel(ctx)
	e.running[task.ID] = cancel
	counts[project.ID]++
	e.background.Go(func() {
		defer func() { e.mu.Lock(); delete(e.running, task.ID); e.mu.Unlock() }()
		if err := e.Step(run, task.ID); stepFailed(err) {
			e.fail(ctx, task.ID, err)
		}
	})
}
func stepFailed(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	return !errors.Is(err, store.ErrLeased) && !errors.Is(err, store.ErrConflict) && !errors.Is(err, ErrProjectPaused)
}
