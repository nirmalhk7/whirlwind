package workflow

import (
	"context"
	"errors"
	"sort"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type RoleQueueAdmission struct {
	RoleWork
	Status string
}

// RequestRoleQueue adapts the dedicated socket to supervisor-owned admission.
// A named item is version-fenced, and an empty pop selects the oldest eligible
// known revision. Only the dispatch loop can acquire its worker lease.
func (e *Engine) RequestRoleQueue(ctx context.Context, action string, work RoleWork) (RoleQueueAdmission, error) {
	if !validRoleQueue(work.Queue) || (action != "push" && action != "pop") {
		return RoleQueueAdmission{}, store.ErrConflict
	}
	if work.PlanID == "" && work.IdeaID == "" && work.Version == 0 && work.ProjectID == "" {
		if action == "push" {
			e.Wake()
			return RoleQueueAdmission{Status: "queued"}, nil
		}
		return e.requestRoleHead(ctx, work.Queue)
	}
	if work.ProjectID == "" && work.PlanID != "" {
		plan, _, err := loadStepInput(ctx, e.Store, work.PlanID)
		if err != nil {
			return RoleQueueAdmission{}, err
		}
		work.ProjectID = plan.ProjectID
	}
	if action == "push" {
		return RoleQueueAdmission{RoleWork: work, Status: "queued"}, e.PushRoleWork(ctx, work)
	}
	return e.requestRolePlan(ctx, work)
}

func validRoleQueue(queue string) bool {
	switch queue {
	case PlannerQueue, CodingQueue, ReviewerQueue, FinalizerQueue:
		return true
	}
	return false
}

func (e *Engine) requestRoleHead(ctx context.Context, queue string) (RoleQueueAdmission, error) {
	records, err := e.Store.List(ctx, "execution_plans")
	if err != nil {
		return RoleQueueAdmission{}, err
	}
	sort.SliceStable(records, func(i, j int) bool { return roleQueueRowBefore(records[i], records[j]) })
	for _, record := range records {
		plan, err := store.Decode[model.ExecutionPlan](record)
		if err != nil || roleQueue(plan.Stage) != queue || e.rolePlanRunning(record.ID) {
			continue
		}
		work := RoleWork{ProjectID: plan.ProjectID, Queue: queue, PlanID: record.ID, Version: record.Version}
		admission, err := e.requestRolePlan(ctx, work)
		if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
			continue
		}
		return admission, err
	}
	return RoleQueueAdmission{Status: "waiting"}, nil
}

func (e *Engine) requestRolePlan(ctx context.Context, work RoleWork) (RoleQueueAdmission, error) {
	scheduled, err := e.PopRoleWork(ctx, work)
	status := "waiting"
	if scheduled {
		status = "scheduled"
	}
	return RoleQueueAdmission{RoleWork: work, Status: status}, err
}
