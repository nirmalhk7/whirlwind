package httpapi

import (
	"context"
	"encoding/json"
	"errors"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (s *Server) executeIdeaUpdate(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	if command.ExpectedVersion < 1 || command.IdempotencyKey == "" {
		return nil, errors.New("expected idea version and idempotency key are required")
	}
	payload := localPayload(command.Payload)
	fingerprint := localFingerprint(command.Operation, command, payload)
	status, cached, err := s.Store.Idempotent(ctx, command.IdempotencyKey, fingerprint)
	if err != nil || status != 0 {
		return cached, err
	}
	record, err := s.Store.Get(ctx, "ideas", command.ResourceID)
	if err != nil {
		return nil, err
	}
	idea, err := store.Decode[model.Idea](record)
	if err != nil {
		return nil, err
	}
	if command.ProjectID == "" || idea.ProjectID != command.ProjectID || record.Version != command.ExpectedVersion {
		return nil, store.ErrConflict
	}
	var draft model.IdeaDraft
	if err := json.Unmarshal(payload, &draft); err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"idea_id": idea.ID, "version": record.Version + 1})
	if err != nil {
		return nil, err
	}
	receipt := &store.Receipt{Key: command.IdempotencyKey, Fingerprint: fingerprint, Status: 200, Body: body}
	if err := s.Engine.UpdateIdea(ctx, idea.ID, record.Version, draft, receipt); err != nil {
		return nil, err
	}
	return body, nil
}
