package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"whirlwind/internal/workflow"
)

func (s *Server) executeRoleQueue(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	var input struct {
		Queue string `json:"queue"`
	}
	decoder := json.NewDecoder(bytes.NewReader(command.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("expected a single role queue payload")
	}
	if command.ProjectID == "" || input.Queue == "" || command.ExpectedVersion < 1 {
		return nil, errors.New("role queue requires a project, queue name and record version")
	}
	work := workflow.RoleWork{ProjectID: command.ProjectID, Queue: input.Queue, PlanID: command.PlanID, IdeaID: command.ResourceID, Version: command.ExpectedVersion}
	status := "queued"
	if command.Operation == "queue.push" {
		if err := s.Engine.PushRoleWork(ctx, work); err != nil {
			return nil, err
		}
	} else {
		ok, err := s.Engine.PopRoleWork(ctx, work)
		if err != nil {
			return nil, err
		}
		status = "waiting"
		if ok {
			status = "scheduled"
		}
	}
	return json.Marshal(map[string]string{"status": status})
}
