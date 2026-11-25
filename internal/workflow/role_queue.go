package workflow

import (
	"context"
	"errors"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	PlannerQueue   = "planner_tasks"
	CodingQueue    = "coding_tasks"
	ReviewerQueue  = "reviewer_tasks"
	FinalizerQueue = "finalizer_tasks"
)

func roleQueue(stage string) string {
	switch stage {
	case "planning", "plan_ready":
		return PlannerQueue
	case "coding", "repair":
		return CodingQueue
	case "review":
		return ReviewerQueue
	case "publishing", "submitting":
		return FinalizerQueue
	}
	return ""
}

type RoleWork struct {
	ProjectID string
	Queue     string
	PlanID    string
	IdeaID    string
	Version   int
}

// PushRoleWork wakes the supervisor for a durable, already-authorized item.
// Its approval or preceding stage writes the queue item transactionally.
func (e *Engine) PushRoleWork(ctx context.Context, work RoleWork) error {
	if work.ProjectID == "" || work.Version < 1 {
		return errors.New("role work requires a project and current record version")
	}
	if work.IdeaID != "" {
		if work.PlanID != "" || work.Queue != PlannerQueue {
			return store.ErrConflict
		}
		record, err := e.Store.Get(ctx, "ideas", work.IdeaID)
		if err != nil {
			return err
		}
		idea, err := store.Decode[model.Idea](record)
		if err != nil {
			return err
		}
		if work.ProjectID != idea.ProjectID || work.Version != record.Version || idea.DecisionStatus != "approved" || idea.ArchivedAt != "" || idea.PlanID != "" {
			return store.ErrConflict
		}
	} else if _, _, err := e.queuedRolePlan(ctx, work); err != nil {
		return err
	}
	e.Wake()
	return nil
}

// PopRoleWork requests admission to a normal supervisor run. The caller never
// acquires a worker lease and cannot return a completion or change the role.
func (e *Engine) PopRoleWork(ctx context.Context, work RoleWork) (bool, error) {
	if _, _, err := e.queuedRolePlan(ctx, work); err != nil {
		return false, err
	}
	request := rolePop{work, ctx, make(chan rolePopResult, 1)}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case e.rolePops <- request:
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case result := <-request.result:
		return result.scheduled, result.err
	}
}

type rolePop struct {
	work   RoleWork
	caller context.Context
	result chan rolePopResult
}

type rolePopResult struct {
	scheduled bool
	err       error
}

func (e *Engine) popRoleWork(ctx context.Context, request rolePop) rolePopResult {
	if err := request.caller.Err(); err != nil {
		return rolePopResult{err: err}
	}
	if err := e.Store.RefreshSettings(ctx); err != nil {
		return rolePopResult{err: err}
	}
	if e.settings(ctx).Stopped {
		return rolePopResult{}
	}
	if err := e.checkDispatchRecovery(ctx); err != nil {
		return rolePopResult{err: err}
	}
	task, record, err := e.queuedRolePlan(request.caller, request.work)
	if err != nil {
		return rolePopResult{err: err}
	}
	records, err := e.Store.List(ctx, "execution_plans")
	if err != nil {
		return rolePopResult{err: err}
	}
	for _, earlier := range records {
		if earlier.CreatedOrder >= record.CreatedOrder {
			continue
		}
		plan, err := store.Decode[model.ExecutionPlan](earlier)
		if err == nil && plan.ProjectID == task.ProjectID && plan.HasSelection() && roleQueue(plan.Stage) == request.work.Queue && !e.rolePlanRunning(plan.ID) {
			return rolePopResult{}
		}
	}
	if err := request.caller.Err(); err != nil {
		return rolePopResult{err: err}
	}
	scheduled, err := e.dispatchTask(ctx, task, e.runningCounts(records), record.Version)
	return rolePopResult{scheduled, err}
}

func (e *Engine) rolePlanRunning(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, running := e.running[id]
	return running
}

func (e *Engine) queuedRolePlan(ctx context.Context, work RoleWork) (model.ExecutionPlan, model.Record, error) {
	var task model.ExecutionPlan
	var record model.Record
	if work.PlanID == "" || work.IdeaID != "" || work.ProjectID == "" || work.Queue == "" || work.Version < 1 {
		return task, record, errors.New("role work requires a project, queue, plan and current record version")
	}
	task, record, err := loadStepInput(ctx, e.Store, work.PlanID)
	if err != nil {
		return task, record, err
	}
	if task.ProjectID != work.ProjectID || record.Version != work.Version || roleQueue(task.Stage) != work.Queue || !task.HasSelection() {
		return task, record, store.ErrConflict
	}
	if work.Queue == PlannerQueue {
		return task, record, nil
	}
	project, err := e.trustedProject(ctx, task.ProjectID)
	if err != nil {
		return task, record, err
	}
	if !localPlanApproved(task) || task.PlanDocument.PolicyHash != project.PolicyHash {
		return task, record, errors.New("role queue requires current local plan approval")
	}
	if err := e.validateApprovalConfiguration(ctx, task); err != nil {
		return task, record, err
	}
	if _, err := e.approvedLocalPlanSnapshot(ctx, task); err != nil {
		return task, record, err
	}
	if work.Queue == FinalizerQueue {
		step := taskStep{Engine: e, ctx: ctx, project: project, task: task, record: record}
		if _, accepted, err := step.acceptedDeliveryReport(); err != nil || !accepted {
			return task, record, errors.Join(errors.New("finalizer queue requires current accepted review and verification"), err)
		}
	}
	return task, record, nil
}
