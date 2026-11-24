package workflow

import (
	"context"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// ConfirmProjectPolicy stages the policy and its setup work in the caller's
// mutation, so a stale or uncommitted request cannot enqueue requalification.
func (e *Engine) ConfirmProjectPolicy(ctx context.Context, project model.Project, version int, rules model.Policy, hash string) error {
	changed := project.PolicyHash != hash
	project.Policy, project.PolicyHash = rules, hash
	if !changed {
		return e.Store.Put(ctx, "projects", project.ID, project, version)
	}
	project.Status, project.QualityIdentity = "draft", nil
	changes, err := e.restartProjectQualification(ctx, project.ID)
	if err != nil {
		return err
	}
	changes = append([]store.Change{{Kind: "projects", ID: project.ID, Value: project, Expected: version}}, changes...)
	if err := e.Store.Write(ctx, changes, nil); err != nil {
		return err
	}
	store.AfterCommit(ctx, e.Wake)
	return nil
}

func (e *Engine) restartProjectQualification(ctx context.Context, projectID string) ([]store.Change, error) {
	records, err := e.Store.List(ctx, "operations")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var changes []store.Change
	for _, record := range records {
		op, err := store.Decode[model.Operation](record)
		if err != nil {
			return nil, err
		}
		if op.Kind != "project_setup" || op.Subject != projectID || (op.Stage != "queued" && op.Stage != "running") {
			continue
		}
		// Retain old evidence, but fence any worker or checkpoint for the old policy.
		op.Stage, op.FinishedAt, op.Error = "cancelled", now, "Project policy changed; superseded by fresh qualification"
		changes = append(changes, store.Change{Kind: "operations", ID: record.ID, Value: op, Expected: record.Version})
	}
	fresh := model.Operation{Kind: "project_setup", Subject: projectID, Stage: "queued", StartedAt: now, Input: map[string]bool{"onboarding": true}}
	return append(changes, store.Change{Kind: "operations", ID: store.ID(), Value: fresh}), nil
}
