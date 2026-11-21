package workflow

import (
	"context"
	"log"
	"sort"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/rolequeue"
	"whirlwind/internal/store"
)

type roleCandidate struct {
	task     model.ExecutionPlan
	delivery rolequeue.Delivery
}

// The committed execution plan doubles as a replayable outbox. The queue is
// reconstructed after a Redis outage, restart, or a lost Redis response.
// A worker never runs against just the message: it rereads the revision under
// the existing SQLite fence. The current revision remains queued until it
// changes; a missed acknowledgement cannot rerun a completed transition.
func (e *Engine) dispatchRoleQueues(ctx context.Context, records []model.Record, counts map[string]int) {
	queues := e.collectRoleCandidates(ctx, records)
	var transportErr error
	for _, role := range rolequeue.Roles() {
		candidates := queues[role]
		deliveries := make([]rolequeue.Delivery, 0, len(candidates))
		for _, item := range candidates {
			deliveries = append(deliveries, item.delivery)
		}
		head, ready, err := e.roleQueues.Head(ctx, role, deliveries)
		if err != nil {
			transportErr = err
			break
		}
		// Never treat Redis as execution authority. With the leased namespace
		// the head can only come from this exact durable candidate set.
		for _, candidate := range candidates {
			if ready && candidate.delivery == head {
				e.dispatchDelivery(ctx, candidate.task, counts, &head)
				break
			}
		}
	}
	e.logRoleQueueError(transportErr)
}

func (e *Engine) collectRoleCandidates(ctx context.Context, records []model.Record) map[rolequeue.Role][]roleCandidate {
	// The last committed revision determines readiness, not creation time of
	// the plan. Redis retains order already assigned to eligible deliveries.
	type orderedRecord struct {
		model.Record
		time time.Time
	}
	ordered := make([]orderedRecord, 0, len(records))
	for _, record := range records {
		stamp, _ := time.Parse(time.RFC3339Nano, record.UpdatedAt)
		ordered = append(ordered, orderedRecord{record, stamp})
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].time.Equal(ordered[j].time) {
			return ordered[i].CreatedOrder < ordered[j].CreatedOrder
		}
		return ordered[i].time.Before(ordered[j].time)
	})
	queues := make(map[rolequeue.Role][]roleCandidate)
	for _, record := range ordered {
		task, err := store.Decode[model.ExecutionPlan](record.Record)
		if err != nil || !runnableStage(task.Stage) {
			continue
		}
		project, err := e.Project(ctx, task.ProjectID)
		if err != nil || project.Paused {
			continue
		}
		if !task.HasSelection() {
			// Preserve the existing adopt/select pause without scheduling an agent.
			_, _ = e.requireSelection(ctx, task, record.Record)
			continue
		}
		role := queuedRole(task, project.PolicyHash)
		if role != "" {
			queues[role] = append(queues[role], roleCandidate{task, deliveryFor(record.Record, task)})
		}
	}
	return queues
}

func queuedRole(task model.ExecutionPlan, policyHash string) rolequeue.Role {
	switch task.Stage {
	case "planning", "plan_ready":
		return rolequeue.Planner
	case "coding", "repair":
		if task.Plan != nil && localPlanApproved(task) {
			return rolequeue.Coding
		}
	case "review":
		if task.Plan != nil && localPlanApproved(task) {
			return rolequeue.Reviewer
		}
	case "publishing", "submitting":
		if taskHasApprovedReview(task) && task.ChecksPassed && task.Score > 80 &&
			(task.Risk != "critical" || approvalMatches(task, policyHash)) {
			return rolequeue.Finalizer
		}
	}
	return ""
}

func deliveryFor(record model.Record, task model.ExecutionPlan) rolequeue.Delivery {
	return rolequeue.Delivery{ID: record.ID, Version: record.Version, Stage: task.Stage, UpdatedAt: record.UpdatedAt}
}

func (e *Engine) logRoleQueueError(err error) {
	if err == nil {
		e.roleQueueError = ""
		return
	}
	if message := err.Error(); message != e.roleQueueError {
		e.roleQueueError = message
		log.Printf("role queue dispatch: %v; supervisor will retry without bypassing Redis", err)
	}
}
