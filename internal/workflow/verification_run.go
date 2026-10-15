package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const verificationPersistenceError = "%w: %v"

type verificationRun struct {
	engine  *Engine
	ctx     context.Context
	task    *model.Task
	id      string
	head    string
	value   map[string]any
	version int
	owner   store.ArtifactOwner
}

func (e *Engine) startVerification(ctx context.Context, p model.Project, t *model.Task) (*verificationRun, error) {
	head, err := github.Run(ctx, t.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	id := store.ID()
	attempts, err := e.verificationAttempts(ctx, t)
	if err != nil {
		return nil, err
	}
	evidence := model.Provenance{Task: t.ID, PlanVersion: t.PlanVersion, Base: t.Base, Head: head, PolicyHash: p.PolicyHash, VerificationID: id, AttemptIDs: attempts}
	value := map[string]any{"provenance": evidence, "task": t.ID, "plan_version": t.PlanVersion, "attempt_id": t.LastAttemptID, "stage": "running", "owner": e.owner, "started_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if err = e.Store.Put(ctx, "verification", id, value, 0); err != nil {
		return nil, fmt.Errorf(verificationPersistenceError, store.ErrPersistence, err)
	}
	t.VerificationID = id
	return &verificationRun{engine: e, ctx: ctx, task: t, id: id, head: head, value: value, version: 1}, nil
}

func (e *Engine) verificationAttempts(ctx context.Context, t *model.Task) ([]string, error) {
	attempts := append([]string{}, t.ImplementationAttempts...)
	records, err := e.Store.List(ctx, "checkpoints")
	if err != nil {
		return nil, fmt.Errorf(verificationPersistenceError, store.ErrPersistence, err)
	}
	for _, record := range records {
		var checkpoint struct {
			Task        string `json:"task"`
			PlanVersion int    `json:"plan_version"`
			AttemptID   string `json:"attempt_id"`
		}
		if err := json.Unmarshal(record.Data, &checkpoint); err != nil {
			return nil, err
		}
		if checkpoint.Task == t.ID && checkpoint.PlanVersion == t.PlanVersion && checkpoint.AttemptID != "" && !slices.Contains(attempts, checkpoint.AttemptID) {
			attempts = append(attempts, checkpoint.AttemptID)
		}
	}
	if t.Plan != nil {
		for _, unit := range t.Plan.Units {
			if unit.AttemptID != "" {
				attempts = append(attempts, unit.AttemptID)
			}
		}
	}
	if t.LastAttemptID != "" && !slices.Contains(attempts, t.LastAttemptID) {
		attempts = append(attempts, t.LastAttemptID)
	}
	return attempts, nil
}

func (run *verificationRun) save() error {
	err := run.engine.Store.Put(context.WithoutCancel(run.ctx), "verification", run.id, run.value, run.version)
	if err == nil {
		run.version++
	}
	if err != nil {
		return fmt.Errorf(verificationPersistenceError, store.ErrPersistence, err)
	}
	return nil
}
