package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (e *Engine) selectedHTTPMCPConnections(ctx context.Context, project model.Project, sources []model.IntegrationSource) ([]mcpBrokerSource, error) {
	connections := make([]mcpBrokerSource, 0, len(sources))
	for _, source := range sources {
		connection, err := e.selectedHTTPMCPConnection(ctx, project, source)
		if err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, nil
}

func (e *Engine) selectedHTTPMCPConnection(ctx context.Context, project model.Project, source model.IntegrationSource) (mcpBrokerSource, error) {
	endpoint, headers, err := e.selectedHTTPMCPDefinition(ctx, project, source)
	if err != nil {
		return mcpBrokerSource{}, err
	}
	upstream, err := newHTTPMCPUpstream(endpoint, headers, project.Policy.Sandbox)
	if err != nil {
		return mcpBrokerSource{}, fmt.Errorf("selected MCP connection %q: %w", source.Ref, err)
	}
	return mcpBrokerSource{Scope: source, Client: upstream}, nil
}

func (e *Engine) selectedHTTPMCPDefinition(ctx context.Context, project model.Project, source model.IntegrationSource) (string, map[string]string, error) {
	configured, err := e.selectedACPProvider(ctx, source.Provider)
	if err != nil {
		return "", nil, err
	}
	definitions, err := provider.NativeMCPDefinitions(configured, project.Path, approvedIntegrationEnvironment(project))
	if err != nil {
		return "", nil, fmt.Errorf("selected integration %q configuration is unavailable: %w", source.Ref, err)
	}
	raw, err := selectedMCPDefinition(definitions, source.Ref)
	if err != nil {
		return "", nil, err
	}
	endpoint, headers, err := parseHTTPMCPDefinition(raw)
	if err != nil {
		return "", nil, fmt.Errorf("selected MCP connection %q: %w", source.Ref, err)
	}
	return endpoint, headers, nil
}

func (e *Engine) selectedACPProvider(ctx context.Context, id string) (model.Provider, error) {
	record, err := e.Store.Get(ctx, "providers", id)
	if err != nil {
		return model.Provider{}, fmt.Errorf("selected integration provider %q is unavailable; configure its supported MCP connection", id)
	}
	configured, err := store.Decode[model.Provider](record)
	if err != nil {
		return model.Provider{}, errors.New("selected integration provider configuration is invalid")
	}
	if configured.Kind != "acp" {
		return model.Provider{}, fmt.Errorf("selected integration provider %q uses unsupported native transport; use an explicitly configured HTTP MCP connection", id)
	}
	return configured, nil
}

func approvedIntegrationEnvironment(project model.Project) []string {
	if project.Policy.Sandbox == nil {
		return nil
	}
	return project.Policy.Sandbox.Environment
}

func selectedMCPDefinition(definitions map[string]json.RawMessage, ref string) (json.RawMessage, error) {
	name := strings.TrimPrefix(ref, "mcp://")
	raw, exists := definitions[name]
	if !exists || name == "" {
		return nil, fmt.Errorf("selected MCP connection %q is not present in the provider's current authorized inventory", ref)
	}
	return raw, nil
}

func newHTTPMCPUpstream(rawURL string, headers map[string]string, policy *model.SandboxPolicy) (*httpMCPUpstream, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !validHTTPMCPURL(parsed) {
		return nil, errors.New("selected integration must use a fixed HTTPS MCP endpoint without embedded credentials or nonstandard port")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if net.ParseIP(host) != nil || strings.Contains(host, "localhost") || !integrationDomainAllowed(host, policy) {
		return nil, errors.New("selected integration endpoint is not covered by the project execution domain allowlist")
	}
	for name, value := range headers {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n:") || strings.ContainsAny(value, "\r\n") || strings.Contains(value, "${") {
			return nil, errors.New("selected integration has unsupported or unresolved HTTP authentication headers")
		}
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &httpMCPUpstream{url: parsed, headers: cloneStringMap(headers), client: client}, nil
}

func validHTTPMCPURL(parsed *url.URL) bool {
	if parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	return parsed.Port() == "" || parsed.Port() == "443"
}

func integrationDomainAllowed(host string, policy *model.SandboxPolicy) bool {
	if policy == nil {
		return false
	}
	for _, allowed := range policy.AllowedDomains {
		allowed = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(allowed, "*.")))
		if allowed == host || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func parseHTTPMCPDefinition(raw json.RawMessage) (string, map[string]string, error) {
	var definition httpMCPDefinition
	if len(raw) == 0 || provider.JSON(string(raw), &definition) != nil || !supportedHTTPMCPDefinition(definition) {
		return "", nil, errors.New("selected integration requires a supported HTTP MCP definition; stdio and native credential transports are unavailable to the read-only broker")
	}
	return definition.URL, definition.Headers, nil
}

type httpMCPDefinition struct {
	Type     string            `json:"type"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Command  string            `json:"command"`
	Env      map[string]string `json:"env"`
	EnvFile  json.RawMessage   `json:"envFile"`
	Auth     json.RawMessage   `json:"auth"`
	Disabled bool              `json:"disabled"`
}

func supportedHTTPMCPDefinition(definition httpMCPDefinition) bool {
	if definition.Disabled || definition.Command != "" || definition.Env != nil || definition.Type != "" && definition.Type != "http" {
		return false
	}
	return len(definition.EnvFile) == 0 && len(definition.Auth) == 0 && definition.URL != ""
}
