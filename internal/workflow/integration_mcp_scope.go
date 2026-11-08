package workflow

import (
	"slices"
	"strings"
)

func mcpScopeBindings(scopes []string, schema map[string]any) (map[string]string, bool) {
	properties, ok := mcpSchemaProperties(schema)
	if !ok || len(scopes) == 0 {
		return nil, false
	}
	bindings := map[string]string{}
	for _, scope := range scopes {
		name, value, ok := mcpScopeProperty(scope, properties, bindings)
		if !ok {
			return nil, false
		}
		bindings[name] = value
	}
	return bindings, len(bindings) == len(scopes) && !hasUnboundScope(properties, bindings)
}

func mcpSchemaProperties(schema map[string]any) (map[string]any, bool) {
	properties, ok := schema["properties"].(map[string]any)
	return properties, ok && len(properties) > 0
}

func mcpScopeProperty(scope string, properties map[string]any, bindings map[string]string) (string, string, bool) {
	kind, value, found := strings.Cut(strings.TrimSpace(scope), ":")
	if !found {
		kind, value = "", strings.TrimSpace(scope)
	}
	aliases := mcpScopeAliases(strings.ToLower(kind))
	if value == "" || len(aliases) == 0 {
		return "", "", false
	}
	matches := matchingScopeProperties(aliases, properties)
	if len(matches) != 1 || bindings[matches[0]] != "" {
		return "", "", false
	}
	return matches[0], value, true
}

func matchingScopeProperties(aliases []string, properties map[string]any) []string {
	matches := []string{}
	for _, alias := range aliases {
		if property, present := properties[alias]; present && mcpSchemaString(property) {
			matches = append(matches, alias)
		}
	}
	return matches
}

func hasUnboundScope(properties map[string]any, bindings map[string]string) bool {
	for property := range properties {
		if slices.Contains(mcpScopeAliases(""), property) && bindings[property] == "" {
			return true
		}
	}
	return false
}

func mcpSchemaString(property any) bool {
	value, ok := property.(map[string]any)
	if !ok {
		return false
	}
	typeName, _ := value["type"].(string)
	return typeName == "string"
}

func mcpScopeAliases(kind string) []string {
	return scopeAliasGroups[kind]
}

var scopeAliasGroups = map[string][]string{
	"repo":         {"repo", "repository", "repository_name", "repositoryName", "repo_name", "repoName", "full_name", "fullName"},
	"repository":   {"repo", "repository", "repository_name", "repositoryName", "repo_name", "repoName", "full_name", "fullName"},
	"":             {"repo", "repository", "repository_name", "repositoryName", "repo_name", "repoName", "full_name", "fullName", "project", "project_key", "projectKey", "project_id", "projectId", "projectIdOrKey", "workspace", "workspace_id", "workspaceId", "organization", "organization_id", "organizationId", "org", "path", "file_path", "filePath", "directory"},
	"project":      {"project", "project_key", "projectKey", "project_id", "projectId", "projectIdOrKey"},
	"project_key":  {"project", "project_key", "projectKey", "project_id", "projectId", "projectIdOrKey"},
	"workspace":    {"workspace", "workspace_id", "workspaceId", "organization", "organization_id", "organizationId", "org"},
	"organization": {"workspace", "workspace_id", "workspaceId", "organization", "organization_id", "organizationId", "org"},
	"org":          {"workspace", "workspace_id", "workspaceId", "organization", "organization_id", "organizationId", "org"},
	"path":         {"path", "file_path", "filePath", "directory"},
	"directory":    {"path", "file_path", "filePath", "directory"},
}
