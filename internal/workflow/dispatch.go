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
	projects, queues := dispatchQueues(records)
	projects, err = e.rotateDispatchProjects(ctx, projects)
	if err != nil {
		return
	}
	e.dispatchProjectQueues(ctx, projects, queues, counts)
}

func dispatchQueues(records []model.Record) ([]string, map[string][]model.ExecutionPlan) {
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
	return projects, queues
}

func (e *Engine) rotateDispatchProjects(ctx context.Context, projects []string) ([]string, error) {
	cursor, err := e.Store.Get(ctx, "dispatch_cursors", "execution_plans")
	if errors.Is(err, store.ErrNotFound) {
		return projects, nil
	}
	if err != nil {
		return nil, err
	}
	last, err := store.Decode[dispatchCursor](cursor)
	if err != nil {
		return nil, err
	}
	n := sort.SearchStrings(projects, last.ProjectID)
	if n < len(projects) && projects[n] == last.ProjectID {
		n++
	}
	return append(append([]string{}, projects[n:]...), projects[:n]...), nil
}

func (e *Engine) dispatchProjectQueues(ctx context.Context, projects []string, queues map[string][]model.ExecutionPlan, counts map[string]int) {
	for {
		remaining := false
		for _, project := range projects {
			queue := queues[project]
			if len(queue) == 0 {
				continue
			}
			remaining = true
			queues[project] = queue[1:]
			e.dispatchTask(ctx, queue[0], counts)
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
func (e *Engine) dispatchTask(ctx context.Context, task model.ExecutionPlan, counts map[string]int) {
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
	globalLimit := e.settings(ctx).GlobalSlots
	if globalLimit <= 0 || len(e.running) >= globalLimit {
		return
	}
	if _, busy := e.running[task.ID]; busy {
		return
	}
	if err := e.advanceDispatchCursor(ctx, task.ProjectID); err != nil {
		return
	}
	run, cancel := context.WithCancel(ctx)
	e.running[task.ID] = cancel
	counts[project.ID]++
	e.background.Go(func() {
		defer func() { e.mu.Lock(); delete(e.running, task.ID); e.mu.Unlock() }()
		if err := e.runStep(run, task.ID, e.failOwnedStep); stepFailed(err) {
			e.fail(ctx, task.ID, err)
		}
	})
}
func (e *Engine) advanceDispatchCursor(ctx context.Context, projectID string) error {
	expected := 0
	if current, err := e.Store.Get(ctx, "dispatch_cursors", "execution_plans"); err == nil {
		expected = current.Version
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return e.Store.Put(ctx, "dispatch_cursors", "execution_plans", dispatchCursor{ProjectID: projectID}, expected)
}

func stepFailed(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	return !errors.Is(err, store.ErrLeased) && !errors.Is(err, store.ErrConflict) && !errors.Is(err, ErrProjectPaused) && !errors.Is(err, ErrPairQueued)
}
