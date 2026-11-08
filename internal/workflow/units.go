package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

type unitRun struct {
	*Engine
	ctx      context.Context
	project  model.Project
	task     model.Task
	unit     *model.Unit
	planText string
}

func unitKey(task string, version int, unit string) string {
	return fmt.Sprintf("%s:%d:%s", task, version, unit)
}

// codeUnit implements one isolated unit or recovers its completed checkpoint, preserving its lease and policy bounds.
func (e *Engine) codeUnit(ctx context.Context, p model.Project, t model.Task, u *model.Unit, plan string) error {
	run := &unitRun{Engine: e, ctx: ctx, project: p, task: t, unit: u, planText: plan}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	run.ctx = ctx
	key := "unit:" + t.ID + ":" + u.ID
	lease, err := e.Store.Claim(ctx, key, e.owner, time.Minute)
	if err != nil {
		return err
	}
	defer func() { _ = e.Store.Release(context.Background(), key, e.owner, lease.Fence) }()
	e.renewLease(ctx, cancel, key, lease.Fence, nil)
	restored, err := run.restoreCheckpoint()
	if restored || err != nil {
		return err
	}
	if err := run.prepareWorktree(); err != nil {
		return err
	}
	if err := run.persistUnitStart(); err != nil {
		return err
	}
	scoped, err := run.scopedProject()
	if err != nil {
		return err
	}
	return run.implement(scoped)
}
func (r *unitRun) prepareWorktree() error {
	if err := ValidateUnitScopes(r.project, []model.Unit{*r.unit}); err != nil {
		return err
	}
	branch, path := unitWorktreeIdentity(r.task, r.unit.ID)
	base := r.unit.Base
	if base == "" {
		var err error
		base, err = github.Run(r.ctx, r.task.Worktree, "git", "rev-parse", "HEAD")
		if err != nil {
			return err
		}
	}
	r.unit.Branch = branch
	r.unit.Worktree = path
	r.unit.Base = base
	return r.worktree(r.ctx, r.project.Path, path, branch, base)
}
func (r *unitRun) restoreCheckpoint() (bool, error) {
	key := unitKey(r.task.ID, r.task.PlanVersion, r.unit.ID)
	record, load := r.Store.Get(r.ctx, "units", key)
	if load == nil {
		previous, err := store.Decode[model.Unit](record)
		if err != nil {
			return false, fmt.Errorf("decode durable unit checkpoint: %w", err)
		}
		if err := r.validateCheckpointIdentity(previous); err != nil {
			return false, err
		}
		r.restoreRuntimeFields(previous)
		if previous.Commit != "" {
			if previous.Stage != "complete" && previous.Stage != "integrated" {
				return false, errors.New("unit checkpoint has a commit in an invalid stage")
			}
			if err := r.validateCheckpointCommit(previous); err != nil {
				return false, err
			}
			return true, nil
		}
		if previous.Stage == "complete" || previous.Stage == "integrated" {
			return false, errors.New("completed unit checkpoint is missing its commit")
		}
	} else if !errors.Is(load, store.ErrNotFound) {
		return false, fmt.Errorf("load durable unit checkpoint: %w", load)
	} else {
		r.clearRuntimeFields()
	}
	return false, nil
}

func unitWorktreeIdentity(task model.Task, unitID string) (string, string) {
	unitName := safe(unitID)
	return task.Branch + "-" + unitName, filepath.Join(filepath.Dir(task.Worktree), unitName)
}

func (r *unitRun) validateCheckpointIdentity(previous model.Unit) error {
	if !unitSpecificationMatches(previous, *r.unit) {
		return errors.New("durable unit checkpoint does not match the approved unit specification")
	}
	wantBranch, wantPath := unitWorktreeIdentity(r.task, r.unit.ID)
	if previous.Branch != wantBranch || previous.Worktree != wantPath || previous.Base == "" {
		return errors.New("durable unit checkpoint has an unexpected worktree, branch, or base")
	}
	if _, err := github.Run(r.ctx, r.task.Worktree, "git", "merge-base", "--is-ancestor", previous.Base, "HEAD"); err != nil {
		return fmt.Errorf("durable unit checkpoint base is outside the current task history: %w", err)
	}
	return nil
}

func unitSpecificationMatches(saved, approved model.Unit) bool {
	return saved.ID == approved.ID && saved.Objective == approved.Objective &&
		slices.Equal(saved.Dependencies, approved.Dependencies) && slices.Equal(saved.AllowedPaths, approved.AllowedPaths)
}

func (r *unitRun) restoreRuntimeFields(previous model.Unit) {
	r.unit.AttemptID = previous.AttemptID
	r.unit.Base = previous.Base
	r.unit.Stage = previous.Stage
	r.unit.Worktree = previous.Worktree
	r.unit.Branch = previous.Branch
	r.unit.Commit = previous.Commit
}

func (r *unitRun) clearRuntimeFields() {
	r.unit.AttemptID = ""
	r.unit.Base = ""
	r.unit.Stage = ""
	r.unit.Worktree = ""
	r.unit.Branch = ""
	r.unit.Commit = ""
}

func (r *unitRun) validateCheckpointCommit(previous model.Unit) error {
	if err := validateExistingWorktree(r.ctx, r.project.Path, previous.Worktree, previous.Branch, previous.Base); err != nil {
		return fmt.Errorf("validate durable unit worktree: %w", err)
	}
	head, err := github.Run(r.ctx, previous.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != previous.Commit {
		return errors.New("durable unit checkpoint commit does not match its worktree HEAD")
	}
	if previous.Stage == "integrated" {
		if _, err = github.Run(r.ctx, r.task.Worktree, "git", "merge-base", "--is-ancestor", previous.Commit, "HEAD"); err != nil {
			return fmt.Errorf("integrated unit checkpoint is not in the task candidate history: %w", err)
		}
	}
	return nil
}

func (e *Engine) validateIntegratedUnitCheckpoints(ctx context.Context, p model.Project, task model.Task) error {
	for _, unit := range task.Plan.Units {
		record, err := e.Store.Get(ctx, "units", unitKey(task.ID, task.PlanVersion, unit.ID))
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("load durable unit checkpoint %s: %w", unit.ID, err)
			}
			if unit.Stage == "integrated" {
				return fmt.Errorf("integrated unit %s has no durable checkpoint", unit.ID)
			}
			continue
		}
		previous, err := store.Decode[model.Unit](record)
		if err != nil {
			return fmt.Errorf("decode durable unit checkpoint %s: %w", unit.ID, err)
		}
		if previous.Stage != "integrated" {
			if unit.Stage == "integrated" {
				return fmt.Errorf("approved unit %s claims integration without a matching durable checkpoint", unit.ID)
			}
			continue
		}
		if !unitSpecificationMatches(previous, unit) {
			return fmt.Errorf("integrated unit %s differs from its approved specification", unit.ID)
		}
		check := unitRun{Engine: e, ctx: ctx, project: p, task: task, unit: &unit}
		if err := check.validateCheckpointIdentity(previous); err != nil {
			return fmt.Errorf("validate integrated unit %s: %w", unit.ID, err)
		}
		if err := check.validateCheckpointCommit(previous); err != nil {
			return fmt.Errorf("validate integrated unit %s: %w", unit.ID, err)
		}
	}
	return nil
}

func (r *unitRun) persistUnitStart() error {
	key := unitKey(r.task.ID, r.task.PlanVersion, r.unit.ID)
	if _, err := r.Store.Get(r.ctx, "units", key); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("load durable unit checkpoint before launch: %w", err)
	}
	r.unit.Stage = "running"
	return r.Store.Put(r.ctx, "units", key, *r.unit, 0)
}
func (r *unitRun) scopedProject() (model.Project, error) {
	p := r.project
	if len(r.unit.AllowedPaths) > 0 {
		for _, path := range r.unit.AllowedPaths {
			if !policy.AllowedPath(p.Policy, path) {
				return p, errors.New("unit scope exceeds project policy")
			}
		}
		p.Policy.AllowedPaths = r.unit.AllowedPaths
	}
	return p, nil
}
func (r *unitRun) implement(p model.Project) error {
	prompt, err := agents.Render("implement", map[string]string{"Unit": jsonText(r.unit), "Plan": r.planText, "Handoff": jsonText(r.task.Handoff)})
	if err != nil {
		return err
	}
	out, err := r.agent(r.ctx, p, r.task, provider.Input{UnitID: r.unit.ID, Role: "code", CWD: r.unit.Worktree, Prompt: prompt})
	if err != nil {
		return err
	}
	if err = r.commit(r.ctx, p, r.unit.Worktree, r.unit.Objective); err != nil {
		return err
	}
	r.unit.Commit, err = github.Run(r.ctx, r.unit.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	r.unit.AttemptID = out.AttemptID
	r.unit.Stage = "complete"
	key := unitKey(r.task.ID, r.task.PlanVersion, r.unit.ID)
	record, _ := r.Store.Get(r.ctx, "units", key)
	return r.Store.Write(r.ctx, []store.Change{{Kind: "units", ID: key, Value: *r.unit, Expected: record.Version}, {Kind: "checkpoints", ID: store.ID(), Value: map[string]any{"task": r.task.ID, "unit": r.unit.ID, "commit": r.unit.Commit, "session": out.SessionID, "attempt_id": out.AttemptID, "plan_version": r.task.PlanVersion}}}, nil)
}
