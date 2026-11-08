package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/execution"
)

func TestCheckMCPHandlerUsesAuthenticatedJSONRPCBrokerEndpoint(t *testing.T) {
	ctx := context.Background()
	s := &providerSession{input: Input{ReadOnly: true, Role: "review"}, hooks: Hooks{RunCheck: func(_ context.Context, id string) (CheckResult, error) {
		return CheckResult{CheckID: id, Passed: true, Output: "approved-check-output"}, nil
	}}}
	token := strings.Repeat("d", 64)
	handler := checkMCPHandler(ctx, s, token)
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	request.Header.Set("X-Whirlwind-MCP-Token", token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"run_check"`) {
		t.Fatalf("check MCP tool list unavailable over broker endpoint: %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run_check","arguments":{"check_id":"tests"}}}`))
	request.Header.Set("X-Whirlwind-MCP-Token", token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "approved-check-output") {
		t.Fatalf("check MCP result unavailable over broker endpoint: %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`))
	request.Header.Set("X-Whirlwind-MCP-Token", "wrong")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("check MCP broker accepted invalid capability: %d", response.Code)
	}
}

// This exercises the actual MCP stdio helper through its advertised schema and Unix broker.
func TestACPCheckMCPAdvertisesAndRunsCommittedID(t *testing.T) {
	ctx := context.Background()
	s := &providerSession{input: Input{ReadOnly: true, Role: "review"}, hooks: Hooks{RunCheck: func(_ context.Context, id string) (CheckResult, error) {
		return CheckResult{CheckID: id, Head: "original-head", Passed: true, Output: "actual-check-result"}, nil
	}}}
	profile, _ := execution.ForPolicy(t.TempDir(), true, "fixture", nil)
	close, err := s.prepareCheckMCP(ctx, &profile)
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	if profile.MCPBroker == nil || profile.MCPBroker.ID != checkMCPBrokerID || profile.MCPBroker.Host != "whirlwind-mcp.openshell.internal" || profile.MCPBroker.Path != "/mcp" || len(profile.MCPBrokerToken) < 32 {
		t.Fatalf("check broker descriptor missing: %+v", profile.MCPBroker)
	}
	server := s.checkServers[0].(map[string]any)
	args := server["args"].([]string)
	if server["command"] != "node" || len(args) != 2 || args[1] != profile.MCPBroker.ID || strings.Contains(strings.Join(args, " "), profile.MCPBrokerToken) || strings.Contains(strings.Join(args, " "), profile.MCPBroker.SocketPath) {
		t.Fatalf("guest helper received host credentials or socket authority: %+v", server)
	}
	if !strings.Contains(strings.Join(profile.ReadRoots, " "), "mcp-stdio.mjs") {
		t.Fatalf("guest MCP shim was not granted as a bounded read root: %v", profile.ReadRoots)
	}
}

func TestCheckMCPRejectsSelectedIntegrationBrokerConflict(t *testing.T) {
	s := &providerSession{input: Input{Role: "code"}, hooks: Hooks{RunCheck: func(context.Context, string) (CheckResult, error) { return CheckResult{}, nil }}}
	profile, err := execution.ForPolicy(t.TempDir(), false, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile.MCPBroker = &execution.MCPBroker{ID: "selected-integration", SocketPath: filepath.Join(t.TempDir(), "broker.sock"), Host: "whirlwind-mcp.openshell.internal", Path: "/mcp"}
	profile.MCPBrokerToken = strings.Repeat("e", 64)
	if _, err := s.prepareCheckMCP(context.Background(), &profile); err == nil || !strings.Contains(err.Error(), "conflicting integration broker") {
		t.Fatalf("check broker silently replaced selected integration broker: %v", err)
	}
}
