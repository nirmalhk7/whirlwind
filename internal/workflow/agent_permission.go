package workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (a *agentAttempt) permission(ctx context.Context, perm provider.Permission) (bool, error) {
	if remotePublicationMutation(perm) {
		return false, a.persistenceError(a.Store.Audit(ctx, model.Audit{Task: a.task.ID, AttemptID: a.id, Source: "policy", Action: "permission_deny", Target: perm.Tool, Version: a.task.PlanVersion, Reason: "remote publication commands are disabled"}))
	}
	if allowed, found, err := a.consumeSavedPermission(ctx, perm); found || err != nil {
		return allowed, err
	}
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

func (a *agentAttempt) consumeSavedPermission(ctx context.Context, perm provider.Permission) (bool, bool, error) {
	records, err := a.Store.List(ctx, "permissions")
	if err != nil {
		return false, false, err
	}
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		var saved pendingPermission
		if err := json.Unmarshal(record.Data, &saved); err != nil {
			return false, false, err
		}
		if !permissionDecisionMatches(saved, a, perm) {
			continue
		}
		originRecord, err := a.Store.Get(ctx, "sessions", saved.AttemptID)
		if err != nil {
			return false, false, err
		}
		origin, err := store.Decode[model.Attempt](originRecord)
		if err != nil {
			return false, false, err
		}
		if origin.Task != a.task.ID || origin.Role != a.input.Role || origin.PlanVersion != a.task.PlanVersion || !origin.Finished {
			continue
		}
		saved.ConsumedBy = a.id
		action := "permission_deny"
		if saved.Decision == "allow" {
			action = "permission_allow"
		}
		audit := model.Audit{Task: a.task.ID, AttemptID: a.id, Source: "local_operator", Action: action, Target: saved.ID, Version: a.task.PlanVersion, Reason: "revalidated exact permission action for fresh attempt"}
		changes := []store.Change{{Kind: "permissions", ID: record.ID, Value: saved, Expected: record.Version}, {Kind: "audit", ID: store.ID(), Value: audit}}
		if err := a.Store.Write(ctx, changes, nil); err != nil {
			return false, true, a.persistenceError(err)
		}
		return saved.Decision == "allow", true, nil
	}
	return false, false, nil
}

func permissionDecisionMatches(saved pendingPermission, attempt *agentAttempt, permission provider.Permission) bool {
	return saved.ID != "" && saved.AttemptID != attempt.id && saved.Task == attempt.task.ID && saved.Provider == attempt.chosen.ID && saved.PlanVersion == attempt.task.PlanVersion && saved.ConsumedBy == "" && (saved.Decision == "allow" || saved.Decision == "deny") && permissionRequestMatches(saved.Request, permission)
}

func remotePublicationMutation(perm provider.Permission) bool {
	tool := strings.ToLower(strings.ReplaceAll(perm.Tool, "-", "_"))
	for _, marker := range []string{"create_pull_request", "create_pr", "pull_request_create", "pr_create", "create_issue", "issue_create", "merge_pull_request", "merge_pr", "push_changes"} {
		if strings.Contains(tool, marker) {
			return true
		}
	}
	if isRemoteMutationCommand(perm.Command, 0) {
		return true
	}
	return false
}

func isRemoteMutationCommand(command []string, depth int) bool {
	if depth > 3 || len(command) == 0 {
		return false
	}
	name := strings.ToLower(filepath.Base(command[0]))
	args := command[1:]
	switch name {
	case "env", "command", "nohup", "sudo":
		for len(args) > 0 && (strings.HasPrefix(args[0], "-") || strings.Contains(args[0], "=")) {
			args = args[1:]
		}
		return isRemoteMutationCommand(args, depth+1)
	case "timeout":
		for len(args) > 0 && (strings.HasPrefix(args[0], "-") || timeoutValue(args[0])) {
			args = args[1:]
		}
		return isRemoteMutationCommand(args, depth+1)
	case "sh", "bash", "zsh", "dash":
		for index, arg := range args {
			if arg == "-c" && index+1 < len(args) {
				return shellHasRemoteMutation(args[index+1], depth+1)
			}
		}
	case "git":
		for _, arg := range args {
			if arg == "push" || arg == "send-pack" {
				return true
			}
		}
	case "gh", "glab":
		return githubCommandMutates(args)
	case "curl", "wget":
		return httpCommandMutates(args)
	}
	return false
}

func timeoutValue(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && char != 's' && char != 'm' && char != 'h' && char != '.' {
			return false
		}
	}
	return true
}

func shellHasRemoteMutation(script string, depth int) bool {
	for _, segment := range strings.FieldsFunc(script, func(char rune) bool {
		return char == ';' || char == '|' || char == '&' || char == '\n'
	}) {
		fields := strings.Fields(strings.TrimSpace(segment))
		for len(fields) > 0 && strings.HasPrefix(fields[0], "(") {
			fields[0] = strings.TrimPrefix(fields[0], "(")
		}
		if len(fields) > 0 && isRemoteMutationCommand(fields, depth+1) {
			return true
		}
	}
	return false
}

func githubCommandMutates(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "api" {
		for index, arg := range args {
			if (arg == "-X" || arg == "--method") && index+1 < len(args) {
				return isWriteMethod(args[index+1])
			}
			if strings.HasPrefix(arg, "--method=") || strings.HasPrefix(arg, "-X") && len(arg) > 2 {
				return isWriteMethod(strings.TrimPrefix(strings.TrimPrefix(arg, "--method="), "-X"))
			}
		}
		return false
	}
	if len(args) < 2 {
		return false
	}
	group, action := args[0], args[1]
	return (group == "pr" && containsString([]string{"create", "merge", "close", "edit", "reopen", "ready", "review", "comment"}, action)) ||
		(group == "issue" && containsString([]string{"create", "close", "edit", "reopen", "comment"}, action)) ||
		(group == "release" && containsString([]string{"create", "delete", "edit", "upload", "delete-asset"}, action)) ||
		(group == "mr" && containsString([]string{"create", "merge", "close", "edit", "approve", "reopen", "note"}, action))
}

func httpCommandMutates(args []string) bool {
	for index, arg := range args {
		if (arg == "-X" || arg == "--request" || arg == "-d" || arg == "--data" || arg == "--data-raw" || arg == "--data-binary" || arg == "--upload-file" || arg == "-T") && index+1 < len(args) {
			if arg == "-X" || arg == "--request" {
				return isWriteMethod(args[index+1])
			}
			return true
		}
		if strings.HasPrefix(arg, "--request=") {
			return isWriteMethod(strings.TrimPrefix(arg, "--request="))
		}
	}
	return false
}

func isWriteMethod(method string) bool {
	return containsString([]string{"POST", "PUT", "PATCH", "DELETE"}, strings.ToUpper(method))
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (a *agentAttempt) requestPermission(ctx context.Context, perm provider.Permission) (bool, error) {
	snapshot, err := store.SafeJSON(perm)
	if err != nil {
		return false, a.persistenceError(err)
	}
	id := store.ID()
	value := map[string]any{"id": id, "attempt_id": a.id, "task": a.task.ID, "provider": a.chosen.ID, "request": json.RawMessage(snapshot), "decision": "pending", "plan_version": a.task.PlanVersion}
	notification := model.Notification{ID: store.ID(), TaskID: a.task.ID, Kind: "permission", Message: "Tool permission awaits decision: " + id}
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Store.Write(persistCtx, []store.Change{{Kind: "permissions", ID: id, Value: value}, {Kind: "inbox", ID: notification.ID, Value: notification}}, nil); err != nil {
		return false, a.persistenceError(err)
	}
	return false, ErrHumanDecisionPending
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
