package provider

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"whirlwind/internal/execution"
)

func (s *providerSession) prepareCheckMCP(ctx context.Context, profile *execution.Profile) (func(), error) {
	if !s.hasScopedTool() {
		return func() {
			// A session without check tools owns no resources to clean up.
		}, nil
	}
	return s.startCheckMCP(ctx, profile)
}

type checkMCPResources struct {
	root, socket, token string
	listener            net.Listener
}

func createCheckMCPResources() (checkMCPResources, error) {
	root, err := os.MkdirTemp("", "ww-check-")
	if err != nil {
		return checkMCPResources{}, err
	}
	tempRoot := root
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return checkMCPResources{}, errors.Join(err, os.RemoveAll(tempRoot))
	}
	socket := filepath.Join(root, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return checkMCPResources{}, errors.Join(err, os.RemoveAll(root))
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return checkMCPResources{}, errors.Join(err, listener.Close(), os.RemoveAll(root))
	}
	return checkMCPResources{root: root, socket: socket, token: hex.EncodeToString(random[:]), listener: listener}, nil
}

func (s *providerSession) startCheckMCP(ctx context.Context, profile *execution.Profile) (func(), error) {
	if profile.MCPBroker != nil || profile.MCPBrokerToken != "" {
		return nil, errors.New("execution profile has a conflicting integration broker; check tools and selected integrations require separate roles")
	}
	resources, err := createCheckMCPResources()
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: checkMCPHandler(ctx, s, resources.token), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(resources.listener) }()
	cleanup := func() { _ = server.Close(); _ = os.RemoveAll(resources.root) }
	if err := s.configureCheckMCP(profile, resources, cleanup); err != nil {
		return nil, err
	}
	return cleanup, nil
}

func checkMCPHandler(ctx context.Context, session *providerSession, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Whirlwind-MCP-Token")), []byte(token)) != 1 {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil || len(raw) > 4096 {
			http.Error(w, "invalid request", 400)
			return
		}
		var request checkRPCRequest
		if err := json.Unmarshal(raw, &request); err != nil || request.JSONRPC != "2.0" || request.Method == "" {
			http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
			return
		}
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result, rpcError := session.dispatchCheckRPC(ctx, request)
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		if rpcError != nil {
			response["error"] = rpcError
		} else {
			response["result"] = result
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	return mux
}

type checkRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (s *providerSession) dispatchCheckRPC(ctx context.Context, request checkRPCRequest) (any, *checkRPCError) {
	switch request.Method {
	case "initialize":
		return initializeCheckRPC(request.Params)
	case "tools/list":
		return map[string]any{"tools": s.checkTools()}, nil
	case "tools/call":
		return s.callCheckRPC(ctx, request.Params)
	default:
		return nil, &checkRPCError{Code: -32601, Message: "unsupported MCP method"}
	}
}

func initializeCheckRPC(raw json.RawMessage) (any, *checkRPCError) {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.ProtocolVersion == "" {
		return nil, &checkRPCError{Code: -32602, Message: "protocolVersion required"}
	}
	return map[string]any{
		"protocolVersion": params.ProtocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]string{"name": "whirlwind_checks", "version": "1"},
	}, nil
}

func (s *providerSession) callCheckRPC(ctx context.Context, raw json.RawMessage) (any, *checkRPCError) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &checkRPCError{Code: -32602, Message: "scoped tool request required"}
	}
	check, err := s.callScopedTool(ctx, params.Name, params.Arguments)
	if err != nil {
		return checkRPCContent(err.Error(), true), nil
	}
	encoded, _ := json.Marshal(check)
	return checkRPCContent(string(encoded), false), nil
}

func checkRPCContent(text string, failed bool) map[string]any {
	return map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}, "isError": failed}
}

type checkRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *providerSession) configureCheckMCP(profile *execution.Profile, resources checkMCPResources, cleanup func()) error {
	script := filepath.Join(filepath.Dir(profile.Launcher), "mcp-stdio.mjs")
	if _, err := os.Stat(script); err != nil {
		cleanup()
		return errors.New("OpenShell guest MCP stdio shim unavailable")
	}
	profile.ReadRoots = append(profile.ReadRoots, script)
	profile.MCPBroker = &execution.MCPBroker{ID: checkMCPBrokerID, SocketPath: resources.socket, Host: "whirlwind-mcp.openshell.internal", Path: "/mcp"}
	profile.MCPBrokerToken = resources.token
	s.checkServers = []any{map[string]any{"name": "whirlwind_checks", "command": "node", "args": []string{script, checkMCPBrokerID}, "env": []any{}}}
	return nil
}

const checkMCPBrokerID = "whirlwind-checks"
