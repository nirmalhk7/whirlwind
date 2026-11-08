package workflow

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
)

const integrationBrokerBodyLimit = 1 << 20

func integrationMCPHandler(ctx context.Context, broker *integrationMCPBroker) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Whirlwind-MCP-Token")), []byte(broker.token)) != 1 {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, integrationBrokerBodyLimit+1))
		if err != nil || len(body) > integrationBrokerBodyLimit {
			http.Error(w, "invalid request", http.StatusRequestEntityTooLarge)
			return
		}
		var request integrationMCPRequest
		if err := json.Unmarshal(body, &request); err != nil || request.JSONRPC != "2.0" || strings.TrimSpace(request.Method) == "" {
			http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
			return
		}
		result, callErr := broker.handle(ctx, request)
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		if callErr != nil {
			response["error"] = map[string]any{"code": -32602, "message": callErr.Error()}
		} else {
			response["result"] = result
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	return mux
}

type integrationMCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (broker *integrationMCPBroker) handle(ctx context.Context, request integrationMCPRequest) (any, error) {
	switch request.Method {
	case "initialize":
		return map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]string{"name": "whirlwind-selected-read-tools", "version": "1"}}, nil
	case "notifications/initialized":
		return nil, nil
	case "tools/list":
		return broker.listTools()
	case "tools/call":
		return broker.callTool(ctx, request.Params)
	default:
		return nil, fmt.Errorf("unsupported broker method %q", request.Method)
	}
}

func (broker *integrationMCPBroker) listTools() (any, error) {
	tools := make([]map[string]any, 0, len(broker.tools))
	for _, tool := range broker.tools {
		tools = append(tools, map[string]any{"name": tool.ID, "description": tool.Description, "inputSchema": tool.InputSchema, "annotations": map[string]bool{"readOnlyHint": true, "destructiveHint": false}})
	}
	slices.SortFunc(tools, func(a, b map[string]any) int { return strings.Compare(a["name"].(string), b["name"].(string)) })
	return map[string]any{"tools": tools}, nil
}

func (broker *integrationMCPBroker) callTool(ctx context.Context, raw json.RawMessage) (any, error) {
	request, err := decodeSelectedToolCall(raw)
	if err != nil {
		return nil, err
	}
	tool, exists := broker.tools[request.Name]
	if !exists {
		broker.recordCall("", request.Name, false)
		return nil, errors.New("tool is not in the selected read-only integration scope")
	}
	if err := validateSelectedToolScope(tool, request.Arguments); err != nil {
		broker.recordCall(tool.Ref, tool.RemoteName, false)
		return nil, err
	}
	return broker.callSelectedTool(ctx, tool, request.Arguments)
}

type selectedToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func decodeSelectedToolCall(raw json.RawMessage) (selectedToolCall, error) {
	var request selectedToolCall
	if err := json.Unmarshal(raw, &request); err != nil || request.Name == "" {
		return selectedToolCall{}, errors.New("invalid selected integration tool request")
	}
	return request, nil
}

func validateSelectedToolScope(tool mcpBrokerTool, arguments map[string]any) error {
	for name, expected := range tool.ScopeBindings {
		value, ok := arguments[name].(string)
		if !ok || strings.TrimSpace(value) != expected {
			return errors.New("tool request is outside the selected integration scope")
		}
	}
	for name := range arguments {
		if slices.Contains(mcpScopeAliases(""), name) && tool.ScopeBindings[name] == "" {
			return errors.New("tool request includes an unselected resource scope")
		}
	}
	return nil
}

func (broker *integrationMCPBroker) callSelectedTool(ctx context.Context, tool mcpBrokerTool, arguments map[string]any) (any, error) {
	result, err := tool.Client.CallTool(ctx, tool.RemoteName, arguments)
	if err != nil {
		broker.recordCall(tool.Ref, tool.RemoteName, false)
		return nil, errors.New("selected integration read failed; check authorized connection and scope")
	}
	broker.recordCall(tool.Ref, tool.RemoteName, true)
	var decoded any
	if err := json.Unmarshal(result, &decoded); err != nil {
		return nil, errors.New("selected integration returned invalid tool data")
	}
	return decoded, nil
}
