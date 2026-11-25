package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

func (s *Server) executeProjectMemory(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	project, role := command.ProjectID, command.ResourceID
	if command.Operation == "inspect.memory" {
		view, err := s.Engine.ReadProjectMemory(ctx, project, role)
		if err != nil {
			return nil, err
		}
		return json.Marshal(view)
	}
	var version int
	var err error
	if command.Operation == "memory.clear" {
		if len(command.Payload) != 0 {
			return nil, errors.New("memory clear takes no payload")
		}
		version, err = s.Engine.ClearProjectMemory(ctx, project, role, command.ExpectedVersion)
	} else {
		var input struct {
			AttemptID string `json:"attempt_id"`
			Note      string `json:"note"`
		}
		decoder := json.NewDecoder(bytes.NewReader(command.Payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return nil, err
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return nil, errors.New("expected a single memory lesson payload")
		}
		if len(input.AttemptID) < 1 || len(input.AttemptID) > 128 {
			return nil, errors.New("memory requires a source attempt ID of at most 128 bytes")
		}
		version, err = s.Engine.RememberProjectLesson(ctx, project, role, input.AttemptID, input.Note, command.ContentHash, command.ExpectedVersion)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]int{"version": version})
}
