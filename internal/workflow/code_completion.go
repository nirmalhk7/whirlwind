package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) finishCode(ctx context.Context, p model.Project, t *model.Task) error {
	t.Handoff.Worktree = t.Worktree
	t.Handoff.Branch = t.Branch
	t.Handoff.NextAction = "verify full implementation"
	if err := e.requireTDDEvidence(ctx, *t); err != nil {
		return err
	}
	if err := e.verify(ctx, p, t); err != nil {
		if errors.Is(err, store.ErrPersistence) {
			return err
		}
		t.Stage = "repair"
		t.Error = err.Error()
		t.ChecksPassed = false
		return e.progress(ctx, t)
	}
	t.Handoff.CompletedWork = append(t.Handoff.CompletedWork, t.Plan.Objective)
	records, _ := e.Store.List(ctx, "checkpoints")
	for _, r := range records {
		var checkpoint struct {
			Task    string `json:"task"`
			Version int    `json:"plan_version"`
		}
		_ = json.Unmarshal(r.Data, &checkpoint)
		if checkpoint.Task == t.ID && checkpoint.Version == t.PlanVersion {
			t.Handoff.Checkpoints = append(t.Handoff.Checkpoints, r.ID)
		}
	}
	return nil
}
