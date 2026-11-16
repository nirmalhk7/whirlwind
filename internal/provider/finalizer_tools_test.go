package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	finalizerToolCallEvent = "item/tool/call"
)

func TestParityFinalizerScopedToolRequiresCorrectRoleAndEmptyArguments(t *testing.T) {
	calls := 0
	s := &providerSession{input: Input{Role: "finalizer", Purpose: "local_finalization", ReadOnly: true}, hooks: Hooks{FinalizeDelivery: func(context.Context) (FinalizationResult, error) {
		calls++
		return FinalizationResult{Branch: "codex/delivery/plan", Commit: "delivery-commit", Tree: "accepted-tree"}, nil
	}}}
	for _, args := range []string{`{"command":["git","push"]}`, `{"plan_id":"another-plan"}`, `{"tree":"other"}`, `null`, `{} {}`} {
		_, err := s.handle(context.Background(), Message{Method: finalizerToolCallEvent, Params: json.RawMessage(`{"tool":"finalize_delivery","arguments":` + args + `}`)})
		if err == nil {
			t.Fatalf("untrusted finalization arguments accepted: %s", args)
		}
	}
	if calls != 0 {
		t.Fatal("invalid request reached trusted Git operation")
	}
	result, err := s.handle(context.Background(), Message{Method: finalizerToolCallEvent, Params: json.RawMessage(`{"tool":"finalize_delivery","arguments":{}}`)})
	if err != nil || result == nil || calls != 1 {
		t.Fatalf("authorized finalization unavailable: %v %v calls=%d", result, err, calls)
	}
	for _, role := range []string{"code", "review", "plan", "ideate"} {
		s.input.Role = role
		if _, err := s.handle(context.Background(), Message{Method: finalizerToolCallEvent, Params: json.RawMessage(`{"tool":"finalize_delivery","arguments":{}}`)}); err == nil {
			t.Fatalf("%s acquired Finalizer authority", role)
		}
	}
	if calls != 1 {
		t.Fatal("another role invoked trusted Git operation")
	}
}

func TestParityFinalizerScopedToolAvailableThroughPrivateMCP(t *testing.T) {
	s := &providerSession{input: Input{Role: "finalizer", Purpose: "local_finalization", ReadOnly: true}, hooks: Hooks{FinalizeDelivery: func(context.Context) (FinalizationResult, error) {
		return FinalizationResult{Branch: "codex/delivery/plan", Commit: "delivery-commit", Tree: "accepted-tree"}, nil
	}}}
	handler := checkMCPHandler(context.Background(), s, "private-token")
	for _, body := range []string{`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"finalize_delivery","arguments":{}}}`} {
		request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		request.Header.Set("X-Whirlwind-MCP-Token", "private-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 || strings.Contains(response.Body.String(), `"error"`) || strings.Contains(response.Body.String(), `"isError":true`) {
			t.Fatalf("private finalization tool denied: %s", response.Body.String())
		}
		if strings.Contains(body, "tools/list") && (!strings.Contains(response.Body.String(), `"name":"finalize_delivery"`) || strings.Contains(response.Body.String(), `"name":"run_check"`)) {
			t.Fatalf("wrong role tools exposed: %s", response.Body.String())
		}
	}
}
