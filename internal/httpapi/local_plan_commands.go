package httpapi

import (
	"context"
	"encoding/json"
	"errors"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// executePlanCommand dispatches directly to the document service. The private
// socket remains the transport; no HTTP request or routing is involved.
func (s *Server) executePlanCommand(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	if _, err := localID(command.PlanID); err != nil {
		return nil, err
	}
	if command.Operation == "plan.document.read" || command.Operation == "inspect.plan" {
		view, err := s.Engine.ReadLocalPlan(ctx, command.PlanID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(view)
	}
	return s.commitLocalCommand(ctx, command, func(ctx context.Context) (any, error) {
		return s.mutatePlanDocument(ctx, command)
	})
}

func (s *Server) commitLocalCommand(ctx context.Context, command LocalCommand, mutate func(context.Context) (any, error)) (json.RawMessage, error) {
	if command.IdempotencyKey == "" || command.ExpectedVersion < 1 {
		return nil, errors.New("expected resource version and idempotency key are required")
	}
	fingerprint := localFingerprint(command.Operation, command, localPayload(command.Payload))
	status, body, err := s.Store.Idempotent(ctx, command.IdempotencyKey, fingerprint)
	if err != nil || status != 0 {
		return body, err
	}
	ctx, batch := store.Stage(ctx)
	result, err := mutate(ctx)
	if err != nil {
		return nil, err
	}
	body, err = json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err := batch.Commit(ctx, s.Store, &store.Receipt{Key: command.IdempotencyKey, Fingerprint: fingerprint, Status: 200, Body: body}); err != nil {
		return nil, err
	}
	if s.Engine != nil {
		s.Engine.Wake()
	}
	return body, nil
}

func (s *Server) mutatePlanDocument(ctx context.Context, command LocalCommand) (any, error) {
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
	if command.ContentHash == "" {
		return nil, errors.New("inspected content hash is required")
	}
	var input localPlanDocumentInput
	if err := json.Unmarshal(localPayload(command.Payload), &input); err != nil {
		return nil, err
	}
	err = s.applyPlanDocumentCommand(ctx, command, record.Version, input)
	if err != nil {
		return nil, err
	}
	return s.Store.Get(ctx, "execution_plans", command.PlanID)
}

type localPlanDocumentInput struct {
	Content  string `json:"content"`
	Action   string `json:"action"`
	Hash     string `json:"hash"`
	Feedback string `json:"feedback"`
}

func (s *Server) applyPlanDocumentCommand(ctx context.Context, command LocalCommand, version int, input localPlanDocumentInput) error {
	if command.Operation == "plan.document.save" {
		return s.Engine.SaveLocalPlan(ctx, command.PlanID, version, command.ContentHash, input.Content)
	} else {
		action := input.Action
		switch command.Operation {
		case "plan.approve":
			action = "approve"
		case "plan.request_changes":
			action = "revise"
		case "plan.reject":
			action = "reject"
		}
		if input.Hash != "" && input.Hash != command.ContentHash {
			return store.ErrConflict
		}
		// All decisions refer to the exact inspected bytes, including rejection.
		view, readErr := s.Engine.ReadLocalPlan(ctx, command.PlanID)
		if readErr != nil {
			return readErr
		}
		if view.Hash != command.ContentHash {
			return store.ErrConflict
		}
		return s.Engine.ReviewLocalPlan(ctx, command.PlanID, version, action, command.ContentHash, input.Feedback)
	}
}
