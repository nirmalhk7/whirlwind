package httpapi

import (
	"context"
	"encoding/json"
	"errors"

	"whirlwind/internal/store"
)

type localAnswerInput struct {
	Answers              map[string]string `json:"answers"`
	ChangesSpecification bool              `json:"changes_specification"`
}

type localPermissionInput struct {
	Decision string `json:"decision"`
}

func (s *Server) executeHumanDecision(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	if err := validateHumanDecisionCommand(command); err != nil {
		return nil, err
	}
	payload := localPayload(command.Payload)
	fingerprint := localFingerprint(command.Operation, command, payload)
	status, cached, err := s.Store.Idempotent(ctx, command.IdempotencyKey, fingerprint)
	if err != nil {
		return nil, err
	}
	if status != 0 {
		return cached, nil
	}
	result, decision, err := decodeHumanDecision(command, payload)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	receipt := &store.Receipt{Key: command.IdempotencyKey, Fingerprint: fingerprint, Status: 200, Body: body}
	if err := s.commitHumanDecision(ctx, command, decision, receipt); err != nil {
		return nil, err
	}
	return body, nil
}

type decodedHumanDecision struct {
	answer     *localAnswerInput
	permission *localPermissionInput
}

func validateHumanDecisionCommand(command LocalCommand) error {
	if command.IdempotencyKey == "" || command.ExpectedVersion < 1 {
		return errors.New("human decision requires idempotency key and expected version")
	}
	return nil
}

func decodeHumanDecision(command LocalCommand, payload []byte) (map[string]string, decodedHumanDecision, error) {
	switch command.Operation {
	case "question.answer":
		var input localAnswerInput
		if err := json.Unmarshal(payload, &input); err != nil {
			return nil, decodedHumanDecision{}, err
		}
		return map[string]string{"status": "answered", "question_id": command.ResourceID}, decodedHumanDecision{answer: &input}, nil
	case "permission.decide":
		var input localPermissionInput
		if err := json.Unmarshal(payload, &input); err != nil {
			return nil, decodedHumanDecision{}, err
		}
		if input.Decision != "allow" && input.Decision != "deny" {
			return nil, decodedHumanDecision{}, errors.New("permission decision must be allow or deny")
		}
		body := map[string]string{"status": "decided", "permission_id": command.ResourceID, "decision": input.Decision}
		return body, decodedHumanDecision{permission: &input}, nil
	default:
		return nil, decodedHumanDecision{}, errors.New("unsupported human decision")
	}
}

func (s *Server) commitHumanDecision(ctx context.Context, command LocalCommand, decision decodedHumanDecision, receipt *store.Receipt) error {
	if decision.answer != nil {
		return s.Engine.AnswerQuestionSetWithReceipt(ctx, command.ResourceID, command.ExpectedVersion, decision.answer.Answers, decision.answer.ChangesSpecification, receipt)
	}
	if decision.permission != nil {
		return s.Engine.DecidePermissionWithReceipt(ctx, command.ResourceID, command.ExpectedVersion, decision.permission.Decision, receipt)
	}
	return errors.New("unsupported human decision")
}
