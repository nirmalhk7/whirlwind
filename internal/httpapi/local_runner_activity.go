package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"whirlwind/internal/model"
)

func (s *Server) executeRunnerActivity(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	return s.commitLocalCommand(ctx, command, func(ctx context.Context) (any, error) {
		if command.ResourceID != "global" {
			return nil, errors.New("runner decision requires inspected global settings resource")
		}
		var input struct {
			Stopped *bool `json:"stopped"`
		}
		if err := json.Unmarshal(localPayload(command.Payload), &input); err != nil {
			return nil, err
		}
		if input.Stopped == nil {
			return nil, errors.New("explicit desired stopped state is required")
		}
		result, err := s.Engine.SetRunnersStopped(ctx, command.ExpectedVersion, *input.Stopped)
		if err != nil {
			return nil, err
		}
		action := "resume_all_runners"
		if *input.Stopped {
			action = "stop_all_runners"
		}
		if err := s.Store.Audit(ctx, model.Audit{Source: "local_operator", Action: action, Target: "runners", Version: command.ExpectedVersion}); err != nil {
			return nil, err
		}
		return result, nil
	})
}
