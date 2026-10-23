package workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type permissionDecision struct {
	Decision string `json:"decision"`
}

func (a *agentAttempt) permission(ctx context.Context, perm provider.Permission) (bool, error) {
	if a.input.NativeIntegrations && perm.Tool == "mcpServer/elicitation/request" {
		var request struct {
			Mode string `json:"mode"`
			URL  string `json:"url"`
		}
		if json.Unmarshal(perm.Raw, &request) == nil && request.Mode == "url" && request.URL != "" {
			return a.requestPermission(ctx, perm)
		}
	}
	if a.input.ReadOnly {
		return false, a.persistenceError(a.Store.Audit(ctx, model.Audit{Task: a.task.ID, AttemptID: a.id, Source: "policy", Action: "permission_deny", Target: perm.Tool, Version: a.task.PlanVersion, Reason: "read-only role"}))
	}
	if a.autoPermission(perm) {
		return true, a.persistenceError(a.Store.Audit(ctx, model.Audit{Task: a.task.ID, AttemptID: a.id, Source: "policy", Action: "permission_allow", Target: perm.Tool, Version: a.task.PlanVersion, Reason: "configured command/path allowlist"}))
	}
	return a.requestPermission(ctx, perm)
}

func (a *agentAttempt) requestPermission(ctx context.Context, perm provider.Permission) (bool, error) {
	snapshot, err := store.SafeJSON(perm)
	if err != nil {
		return false, a.persistenceError(err)
	}
	id := store.ID()
	value := map[string]any{"id": id, "attempt_id": a.id, "task": a.task.ID, "provider": a.chosen.ID, "request": json.RawMessage(snapshot), "decision": "pending", "plan_version": a.task.PlanVersion}
	notification := model.Notification{ID: store.ID(), TaskID: a.task.ID, Kind: "permission", Message: "Tool permission awaits decision: " + id}
	if err := a.Store.Write(ctx, []store.Change{{Kind: "permissions", ID: id, Value: value}, {Kind: "inbox", ID: notification.ID, Value: notification}}, nil); err != nil {
		return false, a.persistenceError(err)
	}
	defer a.cancelPermission(ctx, id)
	decision, err := awaitResource(ctx, a.Store, "permissions", id, func(value permissionDecision) bool { return value.Decision != "pending" })
	return decision.Decision == "allow", err
}
func (a *agentAttempt) autoPermission(perm provider.Permission) bool {
	auto := len(perm.Command) > 0 && policy.AllowedCommand(a.project.Policy, perm.Command)
	if len(perm.Paths) == 0 {
		return auto
	}
	if len(perm.Command) == 0 {
		auto = true
	}
	for _, path := range perm.Paths {
		if !a.allowedPermissionPath(path) {
			return false
		}
	}
	return auto
}
func (a *agentAttempt) allowedPermissionPath(path string) bool {
	if !filepath.IsAbs(path) {
		return policy.AllowedPath(a.project.Policy, path)
	}
	relative, err := filepath.Rel(a.input.CWD, path)
	return err == nil && policy.AllowedPath(a.project.Policy, relative)
}
func (a *agentAttempt) cancelPermission(ctx context.Context, id string) {
	if ctx.Err() == nil {
		return
	}
	record, err := a.Store.Get(context.Background(), "permissions", id)
	if err != nil {
		return
	}
	var value map[string]any
	_ = json.Unmarshal(record.Data, &value)
	if value["decision"] == "pending" {
		value["decision"] = "cancelled"
		_ = a.Store.Put(context.Background(), "permissions", id, value, record.Version)
	}
}
