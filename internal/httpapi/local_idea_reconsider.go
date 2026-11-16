package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (s *Server) executeIdeaReconsider(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	return s.commitLocalCommand(ctx, command, func(ctx context.Context) (any, error) {
		return s.reconsiderIdea(ctx, command)
	})
}

func (s *Server) reconsiderIdea(ctx context.Context, command LocalCommand) (any, error) {
	record, err := s.Store.Get(ctx, "ideas", command.ResourceID)
	if err != nil {
		return nil, err
	}
	idea, err := store.Decode[model.Idea](record)
	if err != nil {
		return nil, err
	}
	if record.Version != command.ExpectedVersion || command.ProjectID == "" || idea.ProjectID != command.ProjectID || idea.ArchivedAt == "" {
		return nil, store.ErrConflict
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(localPayload(command.Payload), &input); err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return nil, errors.New("human reconsideration reason is required")
	}
	if idea.ReconsiderationID != "" {
		prior, err := s.Store.Get(ctx, "proposal_reconsiderations", idea.ReconsiderationID)
		if err != nil {
			return nil, err
		}
		request, err := store.Decode[model.ProposalReconsideration](prior)
		if err != nil {
			return nil, err
		}
		if request.Status == "pending" {
			return nil, errors.New("this idea already has a pending reconsideration request")
		}
	}
	request := model.ProposalReconsideration{ID: store.ID(), ProjectID: idea.ProjectID, PredecessorID: idea.ID, Reason: reason, Status: "pending", RequestedIdeaVersion: record.Version + 1, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	idea.ReconsiderationID = request.ID
	if err := s.Store.Write(ctx, []store.Change{{Kind: "proposal_reconsiderations", ID: request.ID, Value: request}, {Kind: "ideas", ID: idea.ID, Value: idea, Expected: record.Version}}, nil); err != nil {
		return nil, err
	}
	if err := s.Store.Audit(ctx, model.Audit{Source: "local_operator", Action: command.Operation, Target: idea.ID, Version: record.Version, Reason: reason}); err != nil {
		return nil, err
	}
	return s.Store.Get(ctx, "ideas", idea.ID)
}
