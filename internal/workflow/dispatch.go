package workflow

import (
	"context"
	"errors"
	"sort"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// dispatch rotates projects durably and admits oldest work within capacity.
func (e *Engine) dispatch(ctx context.Context) {
	if e.settings(ctx).Stopped {
		return
	}
	e.dispatchProjectSetup(ctx)
	e.intakeApprovedIdeas(ctx)
	records, err := e.Store.List(ctx, "execution_plans")
	if err != nil {
		return
	}
	counts := e.runningCounts(records)
	sort.SliceStable(records, func(i, j int) bool { return records[i].CreatedOrder < records[j].CreatedOrder })
	queues := map[string][]model.ExecutionPlan{}
	var projects []string
	for _, record := range records {
		plan, err := store.Decode[model.ExecutionPlan](record)
		if err == nil && runnableStage(plan.Stage) {
			if _, found := queues[plan.ProjectID]; !found {
				projects = append(projects, plan.ProjectID)
			}
			queues[plan.ProjectID] = append(queues[plan.ProjectID], plan)
		}
	}
	sort.Strings(projects)
	if cursor, err := e.Store.Get(ctx, "dispatch_cursors", "execution_plans"); err == nil {
		last, err := store.Decode[dispatchCursor](cursor)
		if err != nil {
			return
		}
		n := sort.SearchStrings(projects, last.ProjectID)
		if n < len(projects) && projects[n] == last.ProjectID {
			n++
		}
		projects = append(append([]string{}, projects[n:]...), projects[:n]...)
	} else if !errors.Is(err, store.ErrNotFound) {
		return
	}
	for {
		remaining := false
		for _, project := range projects {
			queue := queues[project]
			if len(queue) == 0 {
				continue
			}
			remaining = true
			queues[project] = queue[1:]
			_, _ = e.dispatchTask(ctx, queue[0], counts, 0)
		}
		if !remaining {
			return
		}
	}
}

type dispatchCursor struct {
	ProjectID string `json:"project_id"`
}

func runnableStage(stage string) bool {
	switch stage {
	case "plan_approval", "cancelled", "awaiting_selection", "paused", "questions", "approval", "pr_created", "complete", "error":
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
			task, _ := store.Decode[model.ExecutionPlan](record)
			counts[task.ProjectID]++
		}
	}
	return counts
}
func (e *Engine) dispatchTask(ctx context.Context, task model.ExecutionPlan, counts map[string]int, expected int) (bool, error) {
	record, err := e.Store.Get(ctx, "projects", task.ProjectID)
	if err != nil {
		return false, err
	}
	project, err := store.Decode[model.Project](record)
	if err != nil {
		return false, err
	}
	if project.Paused {
		return false, ErrProjectPaused
	}
	limit := project.Policy.ProjectSlots
	if limit <= 0 {
		limit = e.settings(ctx).ProjectSlots
	}
	if counts[project.ID] >= limit {
		return false, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	settings := e.settings(ctx)
	if e.closing || settings.Stopped || settings.GlobalSlots <= 0 || len(e.running) >= settings.GlobalSlots {
		return false, nil
	}
	if _, busy := e.running[task.ID]; busy {
		return false, nil
	}
	cursorVersion := 0
	if current, err := e.Store.Get(ctx, "dispatch_cursors", "execution_plans"); err == nil {
		cursorVersion = current.Version
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	if err := e.Store.Put(ctx, "dispatch_cursors", "execution_plans", dispatchCursor{ProjectID: task.ProjectID}, cursorVersion); err != nil {
		return false, err
	}
	run, cancel := context.WithCancel(ctx)
	e.running[task.ID] = cancel
	counts[project.ID]++
	e.background.Go(func() {
		defer func() { e.mu.Lock(); delete(e.running, task.ID); e.mu.Unlock() }()
		if err := e.runStepAtVersion(run, task.ID, expected, e.failOwnedStep); expected == 0 && stepFailed(err) {
			e.fail(ctx, task.ID, err)
		}
	})
	return true, nil
}
func stepFailed(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	return !errors.Is(err, store.ErrLeased) && !errors.Is(err, store.ErrConflict) && !errors.Is(err, ErrProjectPaused) && !errors.Is(err, ErrPairQueued)
}
