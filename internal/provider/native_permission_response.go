package provider

func permissionResponse(method string, p map[string]any, allowed bool) any {
	switch method {
	case "mcpServer/elicitation/request":
		return map[string]any{"action": permissionDecision(allowed)}
	case "item/permissions/requestApproval":
		return permissionProfileResponse(p, allowed)
	case "session/request_permission":
		return acpPermissionResponse(p, allowed)
	case "permission/request":
		return map[string]any{"allow": allowed}
	default:
		return map[string]any{"decision": permissionDecision(allowed)}
	}
}

func permissionDecision(allowed bool) string {
	if allowed {
		return "accept"
	}
	return "decline"
}

func permissionProfileResponse(p map[string]any, allowed bool) any {
	permissions := map[string]any{}
	if allowed {
		requested, _ := p["permissions"].(map[string]any)
		for _, key := range []string{"network", "fileSystem"} {
			if value, ok := requested[key].(map[string]any); ok && value != nil {
				permissions[key] = value
			}
		}
	}
	return map[string]any{"permissions": permissions, "scope": "turn"}
}

func acpPermissionResponse(p map[string]any, allowed bool) any {
	options, _ := p["options"].([]any)
	want := "reject_once"
	if allowed {
		want = "allow_once"
	}
	for _, value := range options {
		option, _ := value.(map[string]any)
		if option["kind"] == want {
			return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": option["optionId"]}}
		}
	}
	return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
}
