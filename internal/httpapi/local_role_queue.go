package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"whirlwind/internal/workflow"
)

func (s *Server) executeRunnerControl(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	if command.Operation == "runners.toggle" {
		return s.executeRunnerActivity(ctx, command)
	}
	work, err := decodeRoleWork(command)
	if err != nil {
		return nil, err
	}
	status, err := s.roleQueueStatus(ctx, command.Operation, work)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"status": status})
}

func decodeRoleWork(command LocalCommand) (workflow.RoleWork, error) {
	var input struct {
		Queue string `json:"queue"`
	}
	decoder := json.NewDecoder(bytes.NewReader(command.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return workflow.RoleWork{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return workflow.RoleWork{}, errors.New("expected a single role queue payload")
	}
	if command.ProjectID == "" || input.Queue == "" || command.ExpectedVersion < 1 {
		return workflow.RoleWork{}, errors.New("role queue requires a project, queue name and record version")
	}
	return workflow.RoleWork{ProjectID: command.ProjectID, Queue: input.Queue, PlanID: command.PlanID, IdeaID: command.ResourceID, Version: command.ExpectedVersion}, nil
}

func (s *Server) roleQueueStatus(ctx context.Context, operation string, work workflow.RoleWork) (string, error) {
	if operation == "queue.push" {
		return "queued", s.Engine.PushRoleWork(ctx, work)
	}
	if scheduled, err := s.Engine.PopRoleWork(ctx, work); scheduled || err != nil {
		return "scheduled", err
	}
	return "waiting", nil
}
