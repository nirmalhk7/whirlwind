package httpapi

import (
	"context"
	"encoding/json"
)

func (s *Server) localInspection(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	if command.Operation == "inspect.artifact" {
		return s.inspectArtifact(ctx, command.PlanID, command.ResourceID)
	}
	return s.localProviderStatuses(ctx)
}

func (s *Server) localProviderStatuses(ctx context.Context) (json.RawMessage, error) {
	statuses, err := s.Engine.ProviderStatuses(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(statuses)
}
