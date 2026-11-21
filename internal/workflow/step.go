package workflow

import (
	"context"
	"errors"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/rolequeue"
	"whirlwind/internal/store"
)

type taskStep struct {
	*Engine
	ctx            context.Context
	project        model.Project
	task           model.ExecutionPlan
	record         model.Record
	pending        []store.Change
	reviewAttempts []string
}

// Step owns one task lease and delegates its transition to a focused stage handler.
func (e *Engine) Step(ctx context.Context, id string) error {
	return e.runStep(ctx, id, nil)
}

func (e *Engine) runStep(ctx context.Context, id string, onFailure func(*taskStep, error)) error {
	return e.runDeliveredStep(ctx, id, onFailure, nil)
}

func (e *Engine) runDeliveredStep(ctx context.Context, id string, onFailure func(*taskStep, error), delivery *rolequeue.Delivery) error {
	if e.roleQueues != nil && delivery == nil {
		return errors.New("a Redis role queue delivery is required to run this step")
	}
	if err := e.reconcileStandaloneRuntimes(ctx); err != nil {
		return err
	}
	task, ready, err := e.validateDeliveredStepInput(ctx, id, delivery)
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
	step, err := e.prepareDeliveredStep(ctx, id, delivery)
	if err != nil {
		if step != nil {
			return step.finishStep(err, onFailure)
		}
		return err
	}
	if delivery != nil {
		if queuedRole(step.task, step.project.PolicyHash) == "" {
			return store.ErrConflict
		}
		if err := validateStepPlan(ctx, e, step.task); err != nil {
			return step.finishStep(err, onFailure)
		}
	}
	e.renewStepLease(ctx, cancel, id, lease.Fence, step.record.Version)
	return step.finishStep(step.run(), onFailure)
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
	return store.WithLeaseFence(ctx, "task:"+id, lease), release, lease, nil
}

func (e *Engine) renewStepLease(ctx context.Context, cancel context.CancelFunc, id string, fence int64, version int) {
	e.renewLease(ctx, cancel, "task:"+id, fence, func() bool {
		current, err := e.Store.Get(ctx, "execution_plans", id)
		return err == nil && current.Version == version
	})
}

func (e *Engine) validateStepInput(ctx context.Context, id string) (model.ExecutionPlan, bool, error) {
	return e.validateDeliveredStepInput(ctx, id, nil)
}

func (e *Engine) validateDeliveredStepInput(ctx context.Context, id string, delivery *rolequeue.Delivery) (model.ExecutionPlan, bool, error) {
	task, record, err := loadStepInput(ctx, e.Store, id)
	if err != nil {
		return model.ExecutionPlan{}, false, err
	}
	if delivery != nil && deliveryFor(record, task) != *delivery {
		return task, false, store.ErrConflict
	}
	if !runnableStage(task.Stage) {
		return task, false, nil
	}
	selected, err := e.requireSelection(ctx, task, record)
	if !selected || err != nil {
		return task, selected, err
	}
	if delivery != nil {
		return task, true, nil // defer approval-file validation until the exact revision is leased
	}
	return task, true, validateStepPlan(ctx, e, task)
}

func loadStepInput(ctx context.Context, database *store.Store, id string) (model.ExecutionPlan, model.Record, error) {
	record, err := database.Get(ctx, "execution_plans", id)
	if err != nil {
		return model.ExecutionPlan{}, model.Record{}, err
	}
	task, err := store.Decode[model.ExecutionPlan](record)
	return task, record, err
}

func validateStepPlan(ctx context.Context, engine *Engine, task model.ExecutionPlan) error {
	if task.Stage != "planning" && task.Stage != "plan_ready" && task.Stage != "questions" && task.Stage != "plan_approval" {
		return engine.ValidateLocalPlan(ctx, task)
	}
	return nil
}

func (e *Engine) prepareStep(ctx context.Context, id string) (*taskStep, error) {
	return e.prepareDeliveredStep(ctx, id, nil)
}

func (e *Engine) prepareDeliveredStep(ctx context.Context, id string, delivery *rolequeue.Delivery) (*taskStep, error) {
	r, err := e.Store.Get(ctx, "execution_plans", id)
	if err != nil {
		return nil, err
	}
	t, err := store.Decode[model.ExecutionPlan](r)
	if err != nil {
		return nil, err
	}
	if !runnableStage(t.Stage) || (delivery != nil && deliveryFor(r, t) != *delivery) {
		return nil, store.ErrConflict
	}
	step := &taskStep{Engine: e, ctx: ctx, task: t, record: r}
	step.project, err = e.trustedProject(ctx, t.ProjectID)
	return step, err
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
	step.task.Wait = &model.ExecutionPlanWait{AttemptID: attemptID, Kind: "human_decision", Reason: "Answer the pending agent question or permission request, then resume this plan.", ResumeStage: resumeStage, PlanVersion: step.task.PlanVersion, PolicyHash: step.project.PolicyHash}
	step.task.Handoff.NextAction = "Answer the pending human decision, then resume this plan"
	changes := append(step.pending, store.Change{Kind: "execution_plans", ID: step.task.ID, Value: step.task, Expected: step.record.Version})
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
	return step.Store.Write(step.ctx, append(step.pending, store.Change{Kind: "execution_plans", ID: step.task.ID, Value: step.task, Expected: step.record.Version}), nil)
}
