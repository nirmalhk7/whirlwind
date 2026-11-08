package workflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type mcpRemoteTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
}

type mcpUpstream interface {
	ListTools(context.Context) ([]mcpRemoteTool, error)
	CallTool(context.Context, string, map[string]any) (json.RawMessage, error)
}

type mcpBrokerSource struct {
	Scope  model.IntegrationSource
	Client mcpUpstream
}

type mcpBrokerTool struct {
	ID, Ref, RemoteName string
	Description         string
	InputSchema         map[string]any
	ScopeBindings       map[string]string
	Client              mcpUpstream
}

type integrationMCPBroker struct {
	root, token string
	descriptor  *execution.MCPBroker
	listener    net.Listener
	server      *http.Server
	mu          sync.Mutex
	tools       map[string]mcpBrokerTool
	inventory   []model.Integration
	calls       []provider.ToolEvidence
}

func startIntegrationMCPBroker(ctx context.Context, sources []mcpBrokerSource) (*integrationMCPBroker, error) {
	broker, err := buildIntegrationMCPBroker(ctx, sources)
	if err != nil {
		return nil, err
	}
	root, listener, err := createIntegrationBrokerSocket()
	if err != nil {
		return nil, err
	}
	token, err := newIntegrationBrokerToken()
	if err != nil {
		return nil, errors.Join(err, listener.Close(), os.RemoveAll(root))
	}
	broker.root, broker.token, broker.listener = root, token, listener
	broker.descriptor = &execution.MCPBroker{ID: store.ID(), SocketPath: listener.Addr().String(), Host: "whirlwind-mcp.openshell.internal", Path: "/mcp"}
	broker.server = &http.Server{Handler: integrationMCPHandler(ctx, broker), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() { _ = broker.server.Serve(listener) }()
	return broker, nil
}

func createIntegrationBrokerSocket() (string, net.Listener, error) {
	root, err := os.MkdirTemp("", "whirlwind-mcp-")
	if err != nil {
		return "", nil, errors.New("could not create private integration broker directory")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil, errors.Join(errors.New("could not resolve private integration broker directory"), os.RemoveAll(root))
	}
	root = resolved
	listener, err := net.Listen("unix", filepath.Join(root, "broker.sock"))
	if err != nil {
		return "", nil, errors.Join(errors.New("could not create private integration broker socket"), os.RemoveAll(root))
	}
	if err := os.Chmod(listener.Addr().String(), 0600); err != nil {
		return "", nil, errors.Join(errors.New("could not protect integration broker socket"), listener.Close(), os.RemoveAll(root))
	}
	return root, listener, nil
}

func newIntegrationBrokerToken() (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("could not create integration broker capability")
	}
	return hex.EncodeToString(random[:]), nil
}

func buildIntegrationMCPBroker(ctx context.Context, sources []mcpBrokerSource) (*integrationMCPBroker, error) {
	if len(sources) == 0 {
		return nil, errors.New("read-only integration broker requires selected connections")
	}
	broker := &integrationMCPBroker{tools: map[string]mcpBrokerTool{}, inventory: []model.Integration{}}
	for _, source := range sources {
		item, tools, err := buildIntegrationSource(ctx, source)
		if err != nil {
			return nil, err
		}
		for id, tool := range tools {
			if _, exists := broker.tools[id]; exists {
				return nil, errors.New("selected integration tools have an ambiguous broker identity")
			}
			broker.tools[id] = tool
		}
		broker.inventory = append(broker.inventory, item)
	}
	slices.SortFunc(broker.inventory, func(a, b model.Integration) int { return strings.Compare(a.Ref, b.Ref) })
	return broker, nil
}

func buildIntegrationSource(ctx context.Context, source mcpBrokerSource) (model.Integration, map[string]mcpBrokerTool, error) {
	if err := validateIntegrationSource(source); err != nil {
		return model.Integration{}, nil, err
	}
	remote, err := source.Client.ListTools(ctx)
	if err != nil {
		return model.Integration{}, nil, fmt.Errorf("selected integration %s tool catalog unavailable; check its authorized HTTP MCP connection", source.Scope.Ref)
	}
	return buildIntegrationSourceTools(source, remote)
}

func buildIntegrationSourceTools(source mcpBrokerSource, remote []mcpRemoteTool) (model.Integration, map[string]mcpBrokerTool, error) {
	item := model.Integration{Provider: source.Scope.Provider, Ref: source.Scope.Ref, Name: source.Scope.Ref, Kind: "mcp", Enabled: true, Callable: true}
	tools := make(map[string]mcpBrokerTool)
	for _, tool := range remote {
		brokerTool, supported := scopedReadTool(source, tool)
		if !supported {
			continue
		}
		if _, exists := tools[brokerTool.ID]; exists {
			return model.Integration{}, nil, errors.New("selected integration tools have an ambiguous broker identity")
		}
		item.Tools = append(item.Tools, model.IntegrationTool{Name: tool.Name, Description: tool.Description, ReadOnly: true})
		tools[brokerTool.ID] = brokerTool
	}
	if len(item.Tools) == 0 {
		return model.Integration{}, nil, fmt.Errorf("selected integration %s exposes no tools that are both scope-bound and confirmed read-only", source.Scope.Ref)
	}
	slices.SortFunc(item.Tools, func(a, b model.IntegrationTool) int { return strings.Compare(a.Name, b.Name) })
	return item, tools, nil
}

func validateIntegrationSource(source mcpBrokerSource) error {
	if source.Scope.Provider == "" || source.Scope.Ref == "" || source.Client == nil {
		return errors.New("selected integration is missing a supported upstream connection")
	}
	if len(source.Scope.Scopes) == 0 {
		return fmt.Errorf("selected integration %s needs an explicit read scope", source.Scope.Ref)
	}
	return nil
}

func scopedReadTool(source mcpBrokerSource, tool mcpRemoteTool) (mcpBrokerTool, bool) {
	bindings, supported := mcpScopeBindings(source.Scope.Scopes, tool.InputSchema)
	if !supported || !mcpToolReadOnly(tool) {
		return mcpBrokerTool{}, false
	}
	id := integrationBrokerToolID(source.Scope.Provider, source.Scope.Ref, tool.Name)
	return mcpBrokerTool{ID: id, Ref: source.Scope.Ref, RemoteName: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema, ScopeBindings: bindings, Client: source.Client}, true
}

func mcpToolReadOnly(tool mcpRemoteTool) bool {
	readOnly, _ := tool.Annotations["readOnlyHint"].(bool)
	destructive, _ := tool.Annotations["destructiveHint"].(bool)
	return tool.Name != "" && readOnly && !destructive && tool.InputSchema["type"] == "object"
}

func integrationBrokerToolID(providerID, ref, name string) string {
	parts := []string{providerID, ref, name}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "selected_" + hex.EncodeToString(sum[:8])
}

func (broker *integrationMCPBroker) close() error {
	if broker == nil {
		return nil
	}
	var err error
	if broker.server != nil {
		err = broker.server.Close()
	}
	if broker.listener != nil {
		err = errors.Join(err, broker.listener.Close())
	}
	if broker.root != "" {
		err = errors.Join(err, os.RemoveAll(broker.root))
	}
	return err
}

func (broker *integrationMCPBroker) callsSnapshot() []provider.ToolEvidence {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	return slices.Clone(broker.calls)
}

func (broker *integrationMCPBroker) inventorySnapshot() []model.Integration {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	return slices.Clone(broker.inventory)
}

func (broker *integrationMCPBroker) recordCall(ref, name string, success bool) {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.calls = append(broker.calls, provider.ToolEvidence{Ref: ref, Tool: name, CallID: store.ID(), Success: success})
}
