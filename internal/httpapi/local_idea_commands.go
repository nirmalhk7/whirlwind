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

func (s *Server) executeIdeaDecision(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	return s.commitLocalCommand(ctx, command, func(ctx context.Context) (any, error) {
		record, err := s.Store.Get(ctx, "ideas", command.ResourceID)
		if err != nil {
			return nil, err
		}
		idea, err := store.Decode[model.Idea](record)
		if err != nil {
			return nil, err
		}
		if record.Version != command.ExpectedVersion || command.ProjectID == "" || idea.ProjectID != command.ProjectID {
			return nil, store.ErrConflict
		}
		if idea.ArchivedAt != "" {
			return nil, store.ErrConflict
		}
		if err := applyIdeaDecision(&idea, command); err != nil {
			return nil, err
		}
		if err := s.Store.Put(ctx, "ideas", idea.ID, idea, record.Version); err != nil {
			return nil, err
		}
		if err := s.Store.Audit(ctx, model.Audit{Source: "local_operator", Action: command.Operation, Target: idea.ID, Version: record.Version, Reason: idea.ArchiveReason}); err != nil {
			return nil, err
		}
		return s.Store.Get(ctx, "ideas", idea.ID)
	})
}

func applyIdeaDecision(idea *model.Idea, command LocalCommand) error {
	switch command.Operation {
	case "idea.approve":
		if idea.DecisionStatus != "draft" {
			return store.ErrConflict
		}
		idea.DecisionStatus = "approved"
	case "idea.retry_planning":
		if idea.DecisionStatus != "error" || idea.PlanID != "" {
			return store.ErrConflict
		}
		idea.DecisionStatus, idea.ErrorReason = "approved", ""
	case "idea.archive":
		return archiveIdeaDecision(idea, command.Payload)
	default:
		return errors.New("unsupported idea decision")
	}
	return nil
}

func archiveIdeaDecision(idea *model.Idea, payload json.RawMessage) error {
	if idea.DecisionStatus == "in_process" {
		return errors.New("cancel active planning before archival")
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(localPayload(payload), &input); err != nil {
		return err
	}
	if strings.TrimSpace(input.Reason) == "" {
		return errors.New("archival reason is required")
	}
	idea.ArchivedAt, idea.ArchiveReason = time.Now().UTC().Format(time.RFC3339Nano), strings.TrimSpace(input.Reason)
	return nil
}
