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
	if err := e.reconcileStandaloneRuntimes(ctx); err != nil {
		return err
	}
	task, ready, err := e.validateStepInput(ctx, id)
	if err != nil {
		return err
	}
	if !ready {
		return nil
	}
	ctx, release, lease, err := e.acquireStepLease(ctx, id, task.ProjectID)
	if err != nil {
		return err
	}
	defer release()
	defer func() { _ = e.Store.Release(context.Background(), "task:"+id, e.owner, lease.Fence) }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	step, err := e.prepareStep(ctx, id)
	if err != nil {
		return err
	}
	e.renewStepLease(ctx, cancel, id, lease.Fence, step.record.Version)
	return step.run()
}

func (e *Engine) acquireStepLease(ctx context.Context, id, projectID string) (context.Context, func(), store.Lease, error) {
	ctx, release, err := e.projectRunnerContext(ctx, projectID)
	if err != nil {
		return nil, nil, store.Lease{}, err
	}
	lease, err := e.Store.Claim(ctx, "task:"+id, e.owner, time.Minute)
	if err != nil {
		release()
		return nil, nil, store.Lease{}, err
	}
	return ctx, release, lease, nil
}

func (e *Engine) renewStepLease(ctx context.Context, cancel context.CancelFunc, id string, fence int64, version int) {
	e.renewLease(ctx, cancel, "task:"+id, fence, func() bool {
		current, err := e.Store.Get(ctx, "tasks", id)
		return err == nil && current.Version == version
	})
}

func (e *Engine) validateStepInput(ctx context.Context, id string) (model.Task, bool, error) {
	task, record, err := loadStepInput(ctx, e.Store, id)
	if err != nil {
		return model.Task{}, false, err
	}
	if task.Cancelled() {
		return task, false, nil
	}
	selected, err := e.requireSelection(ctx, task, record)
	if !selected || err != nil {
		return task, selected, err
	}
	return task, true, validateStepPlan(ctx, e, task)
}

func loadStepInput(ctx context.Context, database *store.Store, id string) (model.Task, model.Record, error) {
	record, err := database.Get(ctx, "tasks", id)
	if err != nil {
		return model.Task{}, model.Record{}, err
	}
	task, err := store.Decode[model.Task](record)
	return task, record, err
}

func validateStepPlan(ctx context.Context, engine *Engine, task model.Task) error {
	if task.Stage != "planning" && task.Stage != "plan_ready" && task.Stage != "questions" && task.Stage != "plan_approval" {
		return engine.ValidateLocalPlan(ctx, task)
	}
	return nil
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

func (step *taskStep) pauseForHumanDecision() error {
	resumeStage := step.task.Stage
	attemptID, err := latestPendingDecisionAttempt(context.Background(), step.Store, step.task.ID, step.task.PlanVersion)
	if err != nil {
		return err
	}
	step.task.ResumeStage = resumeStage
	step.task.Stage = "paused"
	step.task.Error = "Human decision pending"
	step.task.Wait = &model.TaskWait{AttemptID: attemptID, Kind: "human_decision", Reason: "Answer the pending agent question or permission request, then resume this plan.", ResumeStage: resumeStage, PlanVersion: step.task.PlanVersion, PolicyHash: step.project.PolicyHash}
	step.task.Handoff.NextAction = "Answer the pending human decision, then resume this plan"
	changes := append(step.pending, store.Change{Kind: "tasks", ID: step.task.ID, Value: step.task, Expected: step.record.Version})
	return step.Store.Write(step.ctx, changes, nil)
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
	finished, err := handler()
	if errors.Is(err, ErrHumanDecisionPending) {
		return step.pauseForHumanDecision()
	}
	if finished || err != nil {
		return err
	}
	if err := step.ctx.Err(); err != nil {
		return err
	}
	return step.Store.Write(step.ctx, append(step.pending, store.Change{Kind: "tasks", ID: step.task.ID, Value: step.task, Expected: step.record.Version}), nil)
}
