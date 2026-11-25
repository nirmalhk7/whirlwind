package httpapi

import (
	"context"
	"encoding/json"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

func (s *Server) executeIdeationStart(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	return s.commitLocalCommand(ctx, command, func(ctx context.Context) (any, error) {
		return s.startIdeation(ctx, command)
	})
}

func (s *Server) startIdeation(ctx context.Context, command LocalCommand) (any, error) {
	record, err := s.Store.Get(ctx, "projects", command.ProjectID)
	if err != nil {
		return nil, err
	}
	project, err := store.Decode[model.Project](record)
	if err != nil {
		return nil, err
	}
	if record.Version != command.ExpectedVersion {
		return nil, store.ErrConflict
	}
	if project.Paused {
		return nil, workflow.ErrProjectPaused
	}
	if err := s.Store.AssertVersion(ctx, "projects", project.ID, record.Version); err != nil {
		return nil, err
	}
	var input struct {
		model.IdeationBrief
		Fresh bool `json:"fresh"`
	}
	if err := json.Unmarshal(localPayload(command.Payload), &input); err != nil {
		return nil, err
	}
	if err := normalizeIdeationBrief(&input.IdeationBrief); err != nil {
		return nil, err
	}
	if err := s.Engine.CheckIdeationPrerequisites(ctx, project); err != nil {
		return nil, err
	}
	if err := s.saveIdeationBrief(ctx, project.ID, input.IdeationBrief); err != nil {
		return nil, err
	}
	day, err := s.Engine.IdeationDay(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	id := store.ID()
	op := model.Operation{Kind: "ideation", Subject: project.ID, Owner: s.Engine.Owner(), Stage: "queued", StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Input: map[string]any{"project_id": project.ID, "day": day, "fresh": input.Fresh}}
	if err := s.Store.Put(ctx, "operations", id, op, 0); err != nil {
		return nil, err
	}
	if err := s.Store.Audit(ctx, model.Audit{Source: "local_operator", Action: command.Operation, Target: project.ID, Version: record.Version}); err != nil {
		return nil, err
	}
	return map[string]string{"operation_id": id}, nil
}

func (s *Server) saveIdeationBrief(ctx context.Context, projectID string, brief model.IdeationBrief) error {
	if brief.Goal != "" || brief.Constraints != "" || brief.Priorities != "" || brief.MaxCandidates > 0 {
		current, err := s.Store.Get(ctx, "ideation_briefs", projectID)
		if err != nil && err != store.ErrNotFound {
			return err
		}
		if err := s.Store.Put(ctx, "ideation_briefs", projectID, brief, current.Version); err != nil {
			return err
		}
	}
	return nil
}
