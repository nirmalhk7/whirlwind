package control

import (
	"context"
	"encoding/json"
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
	DispatchHealthOperation: true,
	"idea.reconsider":       true, "idea.update": true, "idea.approve": true, "idea.archive": true, "idea.retry_planning": true, "planner.cancel": true,
	"plan.document.read": true, "plan.document.save": true, "plan.approve": true, "plan.request_changes": true, "plan.reject": true,
	"inspect.snapshot": true, "inspect.records": true, "inspect.events": true, "inspect.plan": true, "inspect.artifact": true, "supervisor.stop": true,
	"project.register": true, "project.setup.retry": true, "ideation.start": true,
	"project.policy.preview": true, "project.policy.approve": true,
	"provider.status":  true,
	"runners.toggle":   true,
	"direction.select": true, "direction.approve": true,
	"direction.reject": true, "plan.review": true,
	"direction.reconsider": true, "direction.delete": true,
	"question.answer": true, "permission.decide": true,
	"inspect.memory": true, "memory.remember": true, "memory.clear": true,
	"plan.pause": true, "plan.resume": true,
	finalizationInspectOperation: true, "finalization.approve": true,
	RoleQueuePushOperation: true, RoleQueuePopOperation: true,
}
