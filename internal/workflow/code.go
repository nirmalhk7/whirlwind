package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sync/errgroup"
	"path/filepath"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// code executes dependency-ready batches, preserving partial checkpoints before full-task verification.
func (e *Engine) code(ctx context.Context, p model.Project, t *model.Task) error {
	if err := e.prepareWorkspace(ctx, p, t); err != nil {
		return err
	}
	completed := e.restoreUnits(ctx, t)
	for len(completed) < len(t.Plan.Units) {
		ready := readyUnits(t.Plan.Units, completed)
		if len(ready) == 0 {
			return errors.New("no runnable units")
		}
		limit := p.Policy.ProjectSlots
		if limit <= 0 {
			limit = e.settings(ctx).ProjectSlots
		}
		if len(ready) > limit {
			ready = ready[:limit]
		}
		if err := e.runUnits(ctx, p, t, ready); err != nil {
			return err
		}
		if err := e.integrateUnits(ctx, p, t, ready, completed); err != nil {
			return err
		}
	}
	return e.finishCode(ctx, p, t)
}
func (e *Engine) prepareWorkspace(ctx context.Context, p model.Project, t *model.Task) error {
	if t.Worktree == "" {
		e.restoreWorkspace(ctx, t)
	}
	if t.Worktree == "" {
		base, err := e.base(ctx, p)
		if err != nil {
			return err
		}
		t.Base = base
		t.Branch = fmt.Sprintf("codex/whirlwind-%s-v%d", t.ID, t.PlanVersion)
		t.Worktree = filepath.Join(e.Root, "worktrees", t.ID, fmt.Sprintf("v%d", t.PlanVersion), "integration")
		if err = e.worktree(ctx, p.Path, t.Worktree, t.Branch, base); err != nil {
			return err
		}
	}
	key := fmt.Sprintf("%s:%d", t.ID, t.PlanVersion)
	record, _ := e.Store.Get(ctx, "workspaces", key)
	if record.Version == 0 {
		return e.Store.Put(ctx, "workspaces", key, map[string]string{"Base": t.Base, "Branch": t.Branch, "Path": t.Worktree}, 0)
	}
	return nil
}
func (e *Engine) restoreWorkspace(ctx context.Context, t *model.Task) {
	record, err := e.Store.Get(ctx, "workspaces", fmt.Sprintf("%s:%d", t.ID, t.PlanVersion))
	if err != nil {
		return
	}
	var saved struct{ Base, Branch, Path string }
	if json.Unmarshal(record.Data, &saved) == nil {
		t.Base = saved.Base
		t.Branch = saved.Branch
		t.Worktree = saved.Path
	}
}
func (e *Engine) restoreUnits(ctx context.Context, t *model.Task) map[string]bool {
	for n := range t.Plan.Units {
		unit := &t.Plan.Units[n]
		record, err := e.Store.Get(ctx, "units", unitKey(t.ID, t.PlanVersion, unit.ID))
		if err != nil {
			continue
		}
		saved, _ := store.Decode[model.Unit](record)
		if saved.Stage == "integrated" {
			*unit = saved
		}
	}
	completed := map[string]bool{}
	for _, unit := range t.Plan.Units {
		if unit.Stage == "integrated" {
			completed[unit.ID] = true
		}
	}
	return completed
}
func readyUnits(units []model.Unit, completed map[string]bool) []int {
	ready := []int{}
	for n, unit := range units {
		if !completed[unit.ID] && dependenciesComplete(unit.Dependencies, completed) {
			ready = append(ready, n)
		}
	}
	return ready
}
func dependenciesComplete(dependencies []string, completed map[string]bool) bool {
	for _, id := range dependencies {
		if !completed[id] {
			return false
		}
	}
	return true
}
func (e *Engine) runUnits(ctx context.Context, p model.Project, t *model.Task, ready []int) error {
	plan := jsonText(t.Plan)
	snapshot := *t
	snapshot.Plan = nil
	var units errgroup.Group
	for _, n := range ready {
		units.Go(func() error { return e.codeUnit(ctx, p, snapshot, &t.Plan.Units[n], plan) })
	}
	return units.Wait()
}
