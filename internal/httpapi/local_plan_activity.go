package httpapi

import (
	"context"
	"encoding/json"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (s *Server) executePlanActivity(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	return s.commitLocalCommand(ctx, command, func(ctx context.Context) (any, error) {
		record, err := s.Store.Get(ctx, "execution_plans", command.PlanID)
		if err != nil {
			return nil, err
		}
		plan, err := store.Decode[model.ExecutionPlan](record)
		if err != nil {
			return nil, err
		}
		if record.Version != command.ExpectedVersion || command.ProjectID != "" && command.ProjectID != plan.ProjectID {
			return nil, store.ErrConflict
		}
		if err := s.Engine.SetPlanPaused(ctx, command.PlanID, record.Version, command.Operation == "plan.pause"); err != nil {
			return nil, err
		}
		if err := s.Store.Audit(ctx, model.Audit{Task: plan.ID, Source: "local_operator", Action: command.Operation, Target: plan.ID, Version: record.Version}); err != nil {
			return nil, err
		}
		return s.Store.Get(ctx, "execution_plans", plan.ID)
	})
}
