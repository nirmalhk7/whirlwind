package workflow

import (
	"context"
	"errors"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
)

type taskStep struct {
	*Engine
	ctx            context.Context
	project        model.Project
	task           model.Task
	record         model.Record
	pending        []store.Change
	reviewAttempts []string
}

// Step owns one task lease and delegates its transition to a focused stage handler.
func (e *Engine) Step(ctx context.Context, id string) error {
	record, err := e.Store.Get(ctx, "tasks", id)
	if err != nil {
		return err
	}
	task, err := store.Decode[model.Task](record)
	if err != nil {
		return err
	}
	if task.Cancelled() {
		return nil
	}
	if selected, err := e.requireSelection(ctx, task, record); !selected || err != nil {
		return err
	}
	if task.Stage != "planning" && task.Stage != "plan_ready" && task.Stage != "questions" && task.Stage != "plan_approval" {
		if err := e.ValidateLocalPlan(ctx, task); err != nil {
			return err
		}
	}
	ctx, release, err := e.projectRunnerContext(ctx, task.ProjectID)
	if err != nil {
		return err
	}
	defer release()
	lease, err := e.Store.Claim(ctx, "task:"+id, e.owner, time.Minute)
	if err != nil {
		return err
	}
	defer func() { _ = e.Store.Release(context.Background(), "task:"+id, e.owner, lease.Fence) }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	step, err := e.prepareStep(ctx, id)
	if err != nil {
		return err
	}
	e.renewLease(ctx, cancel, "task:"+id, lease.Fence, func() bool {
		current, err := e.Store.Get(ctx, "tasks", id)
		return err == nil && current.Version == step.record.Version
	})
	return step.run()
}
func (e *Engine) prepareStep(ctx context.Context, id string) (*taskStep, error) {
	r, err := e.Store.Get(ctx, "tasks", id)
	if err != nil {
		return nil, err
	}
	t, err := store.Decode[model.Task](r)
	if err != nil {
		return nil, err
	}
	p, err := e.trustedProject(ctx, t.ProjectID)
	if err != nil {
		return nil, err
	}
	return &taskStep{Engine: e, ctx: ctx, project: p, task: t, record: r}, nil
}
func (e *Engine) trustedProject(ctx context.Context, id string) (model.Project, error) {
	p, err := e.Project(ctx, id)
	if err != nil {
		return p, err
	}
	_, hash, err := policy.Load(p.Path)
	if err != nil {
		return p, err
	}
	if hash != p.PolicyHash {
		return p, errors.New("committed policy changed; confirm updated project policy before continuing")
	}
	return p, nil
}

// A finished stage already performed its own final save; otherwise save the transition once.
func (step *taskStep) run() error {
	handlers := map[string]func() (bool, error){
		"planning":   step.planning,
		"plan_ready": step.planReady,
		"coding":     step.coding,
		"review":     step.review,
		"repair":     step.repair,
		"publishing": step.publishing,
		"submitting": step.submitting,
	}
	handler, ok := handlers[step.task.Stage]
	if !ok {
		return nil
	}
	if step.Agentic(step.ctx) && (step.task.Stage == "coding" || step.task.Stage == "repair") {
		if err := step.PreflightPublication(step.ctx, step.project, step.task); err != nil {
			return err
		}
	}
	finished, err := handler()
	if finished || err != nil {
		return err
	}
	if err := step.ctx.Err(); err != nil {
		return err
	}
	return step.Store.Write(step.ctx, append(step.pending, store.Change{Kind: "tasks", ID: step.task.ID, Value: step.task, Expected: step.record.Version}), nil)
}
