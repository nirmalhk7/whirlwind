package httpapi

import (
	"context"
	"encoding/json"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (s *Server) executePlannerCancel(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	result, err := s.commitLocalCommand(ctx, command, func(ctx context.Context) (any, error) {
		record, err := s.Store.Get(ctx, "ideas", command.ResourceID)
		if err != nil {
			return nil, err
		}
		idea, err := store.Decode[model.Idea](record)
		if err != nil {
			return nil, err
		}
		if command.ProjectID == "" || idea.ProjectID != command.ProjectID || command.PlanID != "" && command.PlanID != model.PlanID("idea:"+idea.ID) {
			return nil, store.ErrConflict
		}
		if err := s.Engine.CancelPlanner(ctx, idea.ID, command.ExpectedVersion); err != nil {
			return nil, err
		}
		if err := s.Store.Audit(ctx, model.Audit{Source: "local_operator", Action: command.Operation, Target: idea.ID, Version: record.Version, Reason: model.PlannerCancellationReason}); err != nil {
			return nil, err
		}
		return map[string]any{"idea_id": idea.ID, "version": record.Version + 1, "reason": model.PlannerCancellationReason}, nil
	})
	if err != nil {
		return nil, err
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.Engine.WaitPlannerCleanup(cleanupCtx, command.ResourceID); err != nil {
		return nil, err
	}
	return result, nil
}
