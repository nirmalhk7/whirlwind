package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

func (s *Server) executeLocalProjectData(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	switch command.Operation {
	case "project.policy.preview", "project.policy.approve":
		return s.executeLocalProjectPolicy(ctx, command)
	case "inspect.memory":
		return s.inspectProjectMemory(ctx, command)
	default:
		return s.mutateProjectMemory(ctx, command)
	}
}

func (s *Server) inspectProjectMemory(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	view, err := s.Engine.ReadProjectMemory(ctx, command.ProjectID, command.ResourceID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}

func (s *Server) mutateProjectMemory(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	version, err := s.applyProjectMemoryMutation(ctx, command)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]int{"version": version})
}

func (s *Server) applyProjectMemoryMutation(ctx context.Context, command LocalCommand) (int, error) {
	project, role := command.ProjectID, command.ResourceID
	if command.Operation == "memory.clear" {
		if len(command.Payload) != 0 {
			return 0, errors.New("memory clear takes no payload")
		}
		return s.Engine.ClearProjectMemory(ctx, project, role, command.ExpectedVersion)
	}
	input, err := decodeMemoryLesson(command.Payload)
	if err != nil {
		return 0, err
	}
	return s.Engine.RememberProjectLesson(ctx, project, role, input.AttemptID, input.Note, command.ContentHash, command.ExpectedVersion)
}

type memoryLessonInput struct {
	AttemptID string `json:"attempt_id"`
	Note      string `json:"note"`
}

func decodeMemoryLesson(payload json.RawMessage) (memoryLessonInput, error) {
	var input memoryLessonInput
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return input, errors.New("expected a single memory lesson payload")
	}
	if len(input.AttemptID) < 1 || len(input.AttemptID) > 128 {
		return input, errors.New("memory requires a source attempt ID of at most 128 bytes")
	}
	return input, nil
}
