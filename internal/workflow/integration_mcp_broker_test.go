package workflow

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"whirlwind/internal/model"
)

type fixtureMCPUpstream struct {
	calls atomic.Int64
}

func (*fixtureMCPUpstream) ListTools(context.Context) ([]mcpRemoteTool, error) {
	return []mcpRemoteTool{
		{Name: "read_project", Description: "Read the selected project", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"projectKey": map[string]any{"type": "string"}}, "required": []any{"projectKey"}}, Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "write_project", Description: "Write the selected project", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"projectKey": map[string]any{"type": "string"}}}, Annotations: map[string]any{"readOnlyHint": false}},
		{Name: "read_other", Description: "Unscoped search", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}, Annotations: map[string]any{"readOnlyHint": true}},
	}, nil
}

func (upstream *fixtureMCPUpstream) CallTool(_ context.Context, name string, arguments map[string]any) (json.RawMessage, error) {
	upstream.calls.Add(1)
	data, _ := json.Marshal(map[string]any{"content": []any{map[string]any{"type": "text", "text": "read " + name + " " + arguments["projectKey"].(string)}}})
	return data, nil
}

func TestSelectedIntegrationMCPBrokerFiltersToolsAndEnforcesScope(t *testing.T) {
	upstream := &fixtureMCPUpstream{}
	broker, err := startIntegrationMCPBroker(context.Background(), []mcpBrokerSource{{Scope: model.IntegrationSource{Provider: "fixture", Ref: "mcp://issues", Scopes: []string{"PROJECT-7"}}, Client: upstream}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.close() })
	client := integrationBrokerClient(t, broker)
	list := integrationBrokerCall(t, client, broker, "tools/list", nil)
	var listed struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(list, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Result.Tools) != 1 {
		t.Fatalf("broker exposed write or unscoped MCP tools: %s", list)
	}
	name := listed.Result.Tools[0].Name
	if strings.Contains(string(list), "PROJECT-7") || strings.Contains(string(list), "fixture-secret") {
		t.Fatalf("broker leaked scope credentials or secrets in catalog: %s", list)
	}
	denied := integrationBrokerCall(t, client, broker, "tools/call", map[string]any{"name": "write_project", "arguments": map[string]any{"projectKey": "PROJECT-7"}})
	if !strings.Contains(string(denied), "error") || upstream.calls.Load() != 0 {
		t.Fatalf("write tool reached upstream: response=%s calls=%d", denied, upstream.calls.Load())
	}
	outOfScope := integrationBrokerCall(t, client, broker, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"projectKey": "OTHER-8"}})
	if !strings.Contains(string(outOfScope), "outside the selected integration scope") || upstream.calls.Load() != 0 {
		t.Fatalf("out-of-scope read reached upstream: response=%s calls=%d", outOfScope, upstream.calls.Load())
	}
	additionalScope := integrationBrokerCall(t, client, broker, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"projectKey": "PROJECT-7", "repo": "OTHER/REPO"}})
	if !strings.Contains(string(additionalScope), "unselected resource scope") || upstream.calls.Load() != 0 {
		t.Fatalf("request with an unselected resource scope reached upstream: response=%s calls=%d", additionalScope, upstream.calls.Load())
	}
	valid := integrationBrokerCall(t, client, broker, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"projectKey": "PROJECT-7"}})
	if !strings.Contains(string(valid), "read read_project PROJECT-7") || upstream.calls.Load() != 1 {
		t.Fatalf("selected read was not forwarded: response=%s calls=%d", valid, upstream.calls.Load())
	}
	calls := broker.callsSnapshot()
	if len(calls) != 4 || calls[0].Success || calls[1].Success || calls[2].Success || !calls[3].Success {
		t.Fatalf("broker call evidence lost denials or successful read: %+v", calls)
	}
}

func TestIntegrationBrokerRejectsAmbiguousOrPartiallyBoundScopeSchemas(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"repo": map[string]any{"type": "string"}, "projectKey": map[string]any{"type": "string"}}}
	if _, ok := mcpScopeBindings([]string{"repo:o/r"}, schema); ok {
		t.Fatal("broker accepted a tool whose schema exposed a second unbound resource scope")
	}
	if _, ok := mcpScopeBindings([]string{"repo:o/r", "repo:o/other"}, schema); ok {
		t.Fatal("broker accepted multiple scopes bound to one schema field")
	}
}

func TestSelectedIntegrationMCPBrokerRequiresCapabilityAndSelection(t *testing.T) {
	upstream := &fixtureMCPUpstream{}
	if _, err := buildIntegrationMCPBroker(context.Background(), nil); err == nil {
		t.Fatal("broker accepted an empty selected-source set")
	}
	if _, err := buildIntegrationMCPBroker(context.Background(), []mcpBrokerSource{{Scope: model.IntegrationSource{Provider: "fixture", Ref: "mcp://issues"}, Client: upstream}}); err == nil || !strings.Contains(err.Error(), "explicit read scope") {
		t.Fatalf("broker accepted a selected connection without scope: %v", err)
	}
	broker, err := startIntegrationMCPBroker(context.Background(), []mcpBrokerSource{{Scope: model.IntegrationSource{Provider: "fixture", Ref: "mcp://issues", Scopes: []string{"PROJECT-7"}}, Client: upstream}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.close() })
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	response := httptest.NewRecorder()
	integrationMCPHandler(context.Background(), broker).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("broker accepted request without host-only capability: %d", response.Code)
	}
}

func integrationBrokerClient(t *testing.T, broker *integrationMCPBroker) *http.Client {
	t.Helper()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", broker.descriptor.SocketPath)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

func integrationBrokerCall(t *testing.T, client *http.Client, broker *integrationMCPBroker, method string, params any) []byte {
	t.Helper()
	request := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{JSONRPC: "2.0", ID: 1, Method: method, Params: params}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest, err := http.NewRequest(http.MethodPost, "http://whirlwind-mcp.openshell.internal/mcp", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("X-Whirlwind-MCP-Token", broker.token)
	response, err := client.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		content, _ := io.ReadAll(response.Body)
		t.Fatalf("broker response status %d: %s", response.StatusCode, content)
	}
	returnBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return returnBody
}
