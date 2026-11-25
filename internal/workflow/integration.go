package workflow

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
)

const integrationRevParse = "rev-parse"

func (e *Engine) integrateUnits(ctx context.Context, p model.Project, t *model.ExecutionPlan, ready []int, completed map[string]bool) error {
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
func (e *Engine) mergeUnit(ctx context.Context, p model.Project, t *model.ExecutionPlan, u *model.Unit) (bool, error) {
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
	for _, commit := range strings.Fields(commits) {
		pick := integrationPick{engine: e, task: t, project: p, unit: u, source: commit}
		if err := pick.run(ctx); err != nil {
			return false, err
		}
	}
	return false, nil
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
	if err == nil {
		return false, true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, false, nil
	}
	return false, false, fmt.Errorf("check integrated commit %s: %w", commit, err)
}
