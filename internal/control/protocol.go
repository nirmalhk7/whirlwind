package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	maxIPCMessage = 1 << 20
	maxDiffCursor = 64 << 20
	ipcTimeout    = 15 * time.Second
)

// Request is the bounded local supervisor protocol. Operation is a named
// command, never an HTTP path or arbitrary method dispatch.
type Request struct {
	RequestID       string          `json:"request_id"`
	Operation       string          `json:"operation"`
	ProjectID       string          `json:"project_id,omitempty"`
	PlanID          string          `json:"plan_id,omitempty"`
	ResourceID      string          `json:"resource_id,omitempty"`
	ExpectedVersion int             `json:"expected_version,omitempty"`
	ContentHash     string          `json:"content_hash,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	EventCursor     int64           `json:"event_cursor,omitempty"`
	DiffOffset      int64           `json:"diff_offset,omitempty"`
	Limit           int             `json:"limit,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

type Response struct {
	RequestID string `json:"request_id"`
	Result    any    `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Dispatcher interface {
	Handle(context.Context, Request) (Response, error)
}

var allowedOperations = map[string]bool{
	"idea.reconsider": true, "idea.update": true, "idea.approve": true, "idea.archive": true, "idea.retry_planning": true, "planner.cancel": true,
	"plan.document.read": true, "plan.document.save": true, "plan.approve": true, "plan.request_changes": true, "plan.reject": true,
	"inspect.snapshot": true, "inspect.records": true, "inspect.events": true, "inspect.plan": true, "inspect.artifact": true, "supervisor.stop": true,
	"project.register": true, "project.setup.retry": true, "ideation.start": true,
	"provider.status":  true,
	"runners.toggle":   true,
	"direction.select": true, "direction.approve": true,
	"direction.reject": true, "plan.review": true,
	"direction.reconsider": true, "direction.delete": true,
	"question.answer": true, "permission.decide": true,
	"plan.pause": true, "plan.resume": true,
	"finalization.inspect": true, "finalization.approve": true,
}

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
	if request.Operation == "inspect.events" {
		if request.EventCursor < 0 || request.Limit < 0 || request.Limit > 500 {
			return errors.New("event cursor or page limit is invalid (limit 1..500)")
		}
		return nil
	}
	if request.Operation == "finalization.inspect" && (request.DiffOffset < 0 || request.DiffOffset > maxDiffCursor) {
		return errors.New("finalization diff cursor is outside the supported range")
	}
	if strings.HasPrefix(request.Operation, "inspect.") || request.Operation == "plan.document.read" || request.Operation == "finalization.inspect" || request.Operation == "provider.status" {
		return nil
	}
	if request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 {
		return errors.New("idempotency_key is required for supervisor commands")
	}
	if request.Operation != "project.register" && request.Operation != "supervisor.stop" && request.Operation != "runners.toggle" && request.ExpectedVersion < 1 {
		return errors.New("expected_version must be positive for this supervisor command")
	}
	if (request.Operation == "plan.review" || request.Operation == "plan.document.save" || request.Operation == "plan.approve" || request.Operation == "plan.request_changes" || request.Operation == "plan.reject") && request.ContentHash == "" {
		return errors.New("content_hash is required for plan review")
	}
	if len(request.Payload) > 0 && !json.Valid(request.Payload) {
		return errors.New("payload must be valid JSON")
	}
	return nil
}
