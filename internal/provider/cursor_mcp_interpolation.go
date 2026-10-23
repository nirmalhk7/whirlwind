package provider

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

// ACP consumes concrete transport values; it does not evaluate Cursor's config
// variables. Expand only inherited environment explicitly approved for this
// execution. Keep resolved credentials in memory and never rewrite native files.
func resolveCursorMCPDefinition(raw json.RawMessage, policy *model.SandboxPolicy) (json.RawMessage, error) {
	var definition map[string]any
	if json.Unmarshal(raw, &definition) != nil || definition == nil {
		return nil, errors.New("native MCP server definition must be a JSON object")
	}
	for _, field := range []string{"command", "args", "env", "url", "headers", "auth"} {
		value, exists := definition[field]
		if !exists {
			continue
		}
		resolved, err := resolveCursorMCPValue(value, policy)
		if err != nil {
			return nil, err
		}
		definition[field] = resolved
	}
	return json.Marshal(definition)
}

func resolveCursorMCPValue(value any, policy *model.SandboxPolicy) (any, error) {
	switch value := value.(type) {
	case string:
		return resolveCursorMCPString(value, policy)
	case []any:
		for i, entry := range value {
			resolved, err := resolveCursorMCPValue(entry, policy)
			if err != nil {
				return nil, err
			}
			value[i] = resolved
		}
	case map[string]any:
		for key, entry := range value {
			resolved, err := resolveCursorMCPValue(entry, policy)
			if err != nil {
				return nil, err
			}
			value[key] = resolved
		}
	}
	return value, nil
}

func resolveCursorMCPString(value string, policy *model.SandboxPolicy) (string, error) {
	var result strings.Builder
	for {
		start := strings.Index(value, "${")
		if start < 0 {
			result.WriteString(value)
			return result.String(), nil
		}
		result.WriteString(value[:start])
		expanded, remaining, err := cursorMCPInterpolation(value[start+2:], policy)
		if err != nil {
			return "", err
		}
		result.WriteString(expanded)
		value = remaining
	}
}

func cursorMCPInterpolation(value string, policy *model.SandboxPolicy) (string, string, error) {
	end := strings.IndexByte(value, '}')
	if end < 0 || !strings.HasPrefix(value[:end], "env:") {
		return "", "", errors.New("unsupported native MCP interpolation; use ${env:NAME} with sandbox.environment or explicit bounded paths, then refresh")
	}
	name := strings.TrimPrefix(value[:end], "env:")
	expanded, err := cursorMCPEnvironment(name, policy)
	return expanded, value[end+1:], err
}

func cursorMCPEnvironment(name string, policy *model.SandboxPolicy) (string, error) {
	if !execution.EnvironmentOverrideAllowed(name, policy) {
		return "", errors.New("native MCP environment interpolation requires referenced variables in sandbox.environment; configure the execution allowlist and refresh")
	}
	expanded, exists := os.LookupEnv(name)
	if !exists {
		return "", errors.New("approved native MCP environment variable is unset; export it before restarting Whirlwind, then refresh")
	}
	return expanded, nil
}
