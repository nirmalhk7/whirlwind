package workflow

import (
	"context"
	"errors"
	"log"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// fail pauses a failed task at its resumable stage and creates an Inbox recovery notification.
func (e *Engine) fail(ctx context.Context, id string, failure error) {
	if err := e.pauseFailure(ctx, id, failure); err != nil && !errors.Is(err, store.ErrConflict) {
		log.Printf("persist task recovery %s: %v", id, err)
	}
}

func (e *Engine) pauseFailure(ctx context.Context, id string, failure error) error {
	r, err := e.Store.Get(ctx, "execution_plans", id)
	if err != nil {
		return err
	}
	task, err := store.Decode[model.ExecutionPlan](r)
	if err != nil || task.Stage == "paused" || task.Stage == "plan_approval" || task.Cancelled() {
		return err
	}

	var wait *AdmissionWait
	if errors.As(failure, &wait) {
		if task.Stage == "waiting_quota" {
			return nil
		}
		task.Wait = &model.ExecutionPlanWait{Kind: wait.Kind, Reason: wait.Reason, Provider: wait.Provider, Pool: wait.Pool, Role: wait.Role, ResumeStage: task.Stage, NextCheckAt: wait.NextCheck.UTC().Format(time.RFC3339Nano), PlanVersion: task.PlanVersion}
		if project, err := e.Project(ctx, task.ProjectID); err == nil {
			task.Wait.PolicyHash = project.PolicyHash
		}
		task.ResumeStage, task.Stage, task.Error = task.Stage, "waiting_quota", failure.Error()
		if wait.Kind == "setup" || wait.Kind == "policy" || wait.Kind == "authentication" {
			task.Stage = "paused"
		}
	} else {
		task.ResumeStage, task.Stage, task.Error = task.Stage, "paused", failure.Error()
	}
	// Store.Put commits the structured transition notification in the same transaction.
	return e.Store.Put(ctx, "execution_plans", id, task, r.Version)
}
