package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func validateRequest(request Request) error {
	if strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 128 {
		return errors.New("request_id is required and must be at most 128 characters")
	}
	if !allowedOperations[request.Operation] {
		return fmt.Errorf("unsupported local operation %q", request.Operation)
	}
	if len(request.Payload) > maxIPCMessage/2 {
		return errors.New("command payload exceeds 512 KiB")
	}
	if readOnlyOperation(request.Operation) {
		return validateInspection(request)
	}
	return validateMutation(request)
}

func readOnlyOperation(operation string) bool {
	switch operation {
	case "plan.document.read", finalizationInspectOperation, "provider.status", "project.policy.preview":
		return true
	default:
		return strings.HasPrefix(operation, "inspect.")
	}
}

func validateInspection(request Request) error {
	switch request.Operation {
	case "inspect.events":
		if request.EventCursor < 0 || request.Limit < 0 || request.Limit > 500 {
			return errors.New("event cursor or page limit is invalid (limit 1..500)")
		}
	case finalizationInspectOperation:
		if request.DiffOffset < 0 || request.DiffOffset > maxDiffCursor {
			return errors.New("finalization diff cursor is outside the supported range")
		}
	}
	return nil
}

func validateMutation(request Request) error {
	if request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 {
		return errors.New("idempotency_key is required for supervisor commands")
	}
	if request.Operation != "project.register" && request.Operation != "supervisor.stop" && request.Operation != "runners.toggle" && request.ExpectedVersion < 1 {
		return errors.New("expected_version must be positive for this supervisor command")
	}
	if requiresPlanContentHash(request.Operation) && request.ContentHash == "" {
		return errors.New("content_hash is required for plan review")
	}
	if len(request.Payload) > 0 && !json.Valid(request.Payload) {
		return errors.New("payload must be valid JSON")
	}
	return nil
}

func requiresPlanContentHash(operation string) bool {
	switch operation {
	case "plan.review", "plan.document.save", "plan.approve", "plan.request_changes", "plan.reject":
		return true
	default:
		return false
	}
}
