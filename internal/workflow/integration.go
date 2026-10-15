package workflow

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

const integrationRevParse = "rev-parse"

func (e *Engine) integrateUnits(ctx context.Context, p model.Project, t *model.Task, ready []int, completed map[string]bool) error {
	for _, n := range ready {
		unit := &t.Plan.Units[n]
		beforeAttempt := t.LastAttemptID
		skipped, err := e.mergeUnit(ctx, p, t, unit)
		if err != nil {
			return err
		}
		unit.Stage = "integrated"
		if t.LastAttemptID == beforeAttempt {
			t.LastAttemptID = unit.AttemptID
		}
		if unit.AttemptID != "" {
			t.ImplementationAttempts = append(t.ImplementationAttempts, unit.AttemptID)
		}
		if !skipped {
			key := unitKey(t.ID, t.PlanVersion, unit.ID)
			record, _ := e.Store.Get(ctx, "units", key)
			if err = e.Store.Put(ctx, "units", key, *unit, record.Version); err != nil {
				return err
			}
		}
		completed[unit.ID] = true
		if !skipped {
			t.Handoff.Commits = append(t.Handoff.Commits, unit.Commit)
		}
	}
	return nil
}

// mergeUnit reports existing or empty ranges separately to preserve legacy checkpoint bookkeeping.
func (e *Engine) mergeUnit(ctx context.Context, p model.Project, t *model.Task, u *model.Unit) (bool, error) {
	matching, ancestor, err := integrationStatus(ctx, t.Worktree, u.Commit)
	if err != nil {
		return false, err
	}
	if matching || ancestor {
		return ancestor, nil
	}
	commits, err := github.Run(ctx, u.Worktree, "git", "rev-list", "--reverse", u.Base+".."+u.Commit)
	if err != nil {
		return false, err
	}
	if commits == "" {
		return true, nil
	}
	return false, e.cherryPickUnit(ctx, p, t, u, commits)
}
func (e *Engine) cherryPickUnit(ctx context.Context, p model.Project, t *model.Task, u *model.Unit, commits string) error {
	_, err := github.Run(ctx, t.Worktree, "git", append([]string{"cherry-pick", "--keep-redundant-commits"}, strings.Fields(commits)...)...)
	if err == nil {
		return nil
	}
	return e.resolveIntegrationConflict(ctx, p, t, u)
}

func (e *Engine) resolveIntegrationConflict(ctx context.Context, p model.Project, t *model.Task, u *model.Unit) error {
	prompt, err := agents.Render("integrate", map[string]string{"Unit": jsonText(u)})
	if err != nil {
		return err
	}
	out, runErr := e.agent(ctx, p, *t, provider.Input{UnitID: u.ID, Purpose: "integration", Role: "code", CWD: t.Worktree, Prompt: prompt})
	if runErr != nil {
		return runErr
	}
	t.LastAttemptID = out.AttemptID
	t.ImplementationAttempts = append(t.ImplementationAttempts, out.AttemptID)
	if _, err = github.Run(ctx, t.Worktree, "git", integrationRevParse, "--verify", "CHERRY_PICK_HEAD"); err == nil {
		return errors.New("conflict unresolved")
	}
	head, headErr := github.Run(ctx, t.Worktree, "git", integrationRevParse, "HEAD")
	if headErr != nil {
		return headErr
	}
	return e.Store.Put(ctx, "checkpoints", store.ID(), map[string]any{"task": t.ID, "unit": u.ID, "plan_version": t.PlanVersion, "attempt_id": out.AttemptID, "commit": head, "kind": "integration"}, 0)
}

func integrationStatus(ctx context.Context, path, commit string) (bool, bool, error) {
	head, err := github.Run(ctx, path, "git", integrationRevParse, "HEAD")
	if err != nil {
		return false, false, err
	}
	if head == commit {
		return true, false, nil
	}
	err = exec.CommandContext(ctx, "git", "-C", path, "merge-base", "--is-ancestor", commit, "HEAD").Run()
	return false, err == nil, nil
}
