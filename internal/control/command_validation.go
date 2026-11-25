package control

import (
	"encoding/json"
	"errors"
)

func validateCommandFields(request Request) error {
	if request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 {
		return errors.New("idempotency_key is required for supervisor commands")
	}
	if err := validateExpectedVersion(request); err != nil {
		return err
	}
	if needsPlanContentHash(request.Operation) && request.ContentHash == "" {
		return errors.New("content_hash is required for plan review")
	}
	if len(request.Payload) > 0 && !json.Valid(request.Payload) {
		return errors.New("payload must be valid JSON")
	}
	return nil
}

func validateExpectedVersion(request Request) error {
	switch request.Operation {
	case "project.register", "supervisor.stop", "runners.toggle":
	case "memory.remember", "memory.clear":
		if request.ExpectedVersion < 0 {
			return errors.New("memory expected_version cannot be negative")
		}
	default:
		if request.ExpectedVersion < 1 {
			return errors.New("expected_version must be positive for this supervisor command")
		}
	}
	return nil
}

func needsPlanContentHash(operation string) bool {
	switch operation {
	case "plan.review", "plan.document.save", "plan.approve", "plan.request_changes", "plan.reject":
		return true
	default:
		return false
	}
}
