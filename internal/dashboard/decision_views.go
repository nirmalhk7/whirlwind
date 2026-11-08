package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func directionApprovalRequest(direction model.Direction, version int) control.Request {
	return control.Request{Operation: "direction.approve", ResourceID: direction.ID, ProjectID: direction.ProjectID, ExpectedVersion: version, Payload: json.RawMessage(`{}`)}
}

func planReviewRequest(plan model.Task, version int, hash string, payload json.RawMessage) control.Request {
	return control.Request{Operation: "plan.review", PlanID: plan.ID, ProjectID: plan.ProjectID, ExpectedVersion: version, ContentHash: hash, Payload: payload}
}

func permissionSummary(record model.Record) string {
	var saved struct {
		Task        string          `json:"task"`
		AttemptID   string          `json:"attempt_id"`
		Provider    string          `json:"provider"`
		Decision    string          `json:"decision"`
		PlanVersion int             `json:"plan_version"`
		Request     json.RawMessage `json:"request"`
	}
	if json.Unmarshal(record.Data, &saved) != nil {
		return "Saved permission details are unreadable. Refusing to show an ambiguous approval."
	}
	var request struct {
		Tool    string   `json:"tool"`
		Command []string `json:"command"`
		Paths   []string `json:"paths"`
		Raw     any      `json:"raw"`
	}
	if json.Unmarshal(saved.Request, &request) != nil {
		return "Saved permission action is unreadable. Refusing to show an ambiguous approval."
	}
	var details strings.Builder
	fmt.Fprintf(&details, "Saved tool permission\nProvider: %s\nTask: %s\nAttempt: %s\nPlan version: %d\nTool: %s\n", saved.Provider, saved.Task, saved.AttemptID, saved.PlanVersion, request.Tool)
	if len(request.Command) != 0 {
		fmt.Fprintf(&details, "Command: %s\n", strings.Join(request.Command, " "))
	}
	if len(request.Paths) != 0 {
		fmt.Fprintf(&details, "Paths: %s\n", strings.Join(request.Paths, ", "))
	}
	if request.Raw != nil {
		redacted, err := json.Marshal(redactPermissionDetails(request.Raw))
		if err == nil {
			fmt.Fprintf(&details, "Request details: %s\n", redacted)
		}
	}
	return details.String()
}

func redactPermissionDetails(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitivePermissionKey(key) {
				redacted[key] = "[redacted]"
				continue
			}
			redacted[key] = redactPermissionDetails(item)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, item := range typed {
			redacted[index] = redactPermissionDetails(item)
		}
		return redacted
	default:
		return value
	}
}

func sensitivePermissionKey(key string) bool {
	name := strings.ToLower(key)
	for _, marker := range []string{"secret", "token", "password", "authorization", "credential"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func (d *Dashboard) planRecord(id string) (model.Record, model.Task, bool) {
	for _, record := range d.snapshot["tasks"] {
		if record.ID != id {
			continue
		}
		plan, err := store.Decode[model.Task](record)
		return record, plan, err == nil
	}
	return model.Record{}, model.Task{}, false
}

func (d *Dashboard) planStillCurrent(id string, version int, hash string) bool {
	if d.planID != id {
		return false
	}
	record, plan, ok := d.planRecord(id)
	if !ok || record.Version != version {
		return false
	}
	if hash == "" {
		return true
	}
	return plan.PlanDocument != nil && plan.PlanDocument.Hash == hash
}
