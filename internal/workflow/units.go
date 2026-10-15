package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	if err := run.prepareWorktree(); err != nil {
		return err
	}
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
	scoped, err := run.scopedProject()
	if err != nil {
		return err
	}
	return run.implement(scoped)
}
func (r *unitRun) prepareWorktree() error {
	branch := r.task.Branch + "-" + safe(r.unit.ID)
	path := filepath.Join(filepath.Dir(r.task.Worktree), safe(r.unit.ID))
	base, err := github.Run(r.ctx, r.task.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
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
		previous, _ := store.Decode[model.Unit](record)
		if previous.Base != "" {
			r.unit.Base = previous.Base
		}
		if previous.Commit != "" {
			*r.unit = previous
			return true, nil
		}
	} else {
		r.unit.Stage = "running"
		return false, r.Store.Put(r.ctx, "units", key, *r.unit, 0)
	}
	return false, nil
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
