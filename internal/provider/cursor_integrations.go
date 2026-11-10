package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/process"
	"whirlwind/internal/store"
)

// prepareCursorIntegrations keeps native discovery and transport in Cursor's
// own runtime. A CLI error never includes stdout/stderr, which may contain auth.
func (s *providerSession) prepareCursorIntegrations(p model.Provider, profile execution.Profile) error {
	definitions, err := NativeMCPDefinitions(p, s.input.CWD, profile.Environment)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	slices.Sort(names)
	catalog := make([]model.Integration, 0, len(names))
	servers := make([]any, 0, len(names))
	for _, name := range names {
		item := model.Integration{Provider: s.input.ProviderID, Ref: "mcp://" + name, Name: name, Kind: "mcp", Enabled: true}
		server, setupErr := cursorMCPServer(name, definitions[name])
		if setupErr == nil {
			item.Tools, setupErr = s.cursorIntegrationTools(p.Command, name, profile)
		}
		if setupErr != nil {
			if errors.Is(setupErr, execution.ErrIsolationUnverified) {
				return setupErr
			}
			item.Error = setupErr.Error()
		} else {
			item.Callable = true
			servers = append(servers, server)
		}
		catalog = append(catalog, item)
	}
	s.output.Integrations = catalog
	if err := s.checkIntegrationInventory(catalog); err != nil {
		return err
	}
	s.nativeServers = servers
	return nil
}

func (s *providerSession) cursorIntegrationTools(command []string, name string, profile execution.Profile) ([]model.IntegrationTool, error) {
	args, err := cursorListToolsCommand(command, name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer cancel()
	probeProfile, err := pinCursorProbeProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	data, err := cursorNativeToolProbe(ctx, args, probeProfile)
	if err != nil {
		return nil, err
	}
	return cursorToolCatalog(data)
}

func pinCursorProbeProfile(ctx context.Context, parent execution.Profile) (execution.Profile, error) {
	if !execution.IsOpenShellLauncher(parent.Launcher) {
		return parent, nil
	}
	registryDir, err := cursorStandaloneRegistryDir(parent)
	if err != nil {
		return execution.Profile{}, err
	}
	parentDeadline, err := time.Parse(time.RFC3339Nano, parent.DeadlineAt)
	if err != nil || !parentDeadline.After(time.Now()) {
		return execution.Profile{}, fmt.Errorf("%w: Cursor probe requires the parent execution's live deadline", execution.ErrIsolationUnverified)
	}
	probeDeadline := parentDeadline
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(probeDeadline) {
		probeDeadline = ctxDeadline
	}
	if !probeDeadline.After(time.Now()) {
		return execution.Profile{}, context.DeadlineExceeded
	}
	name, endpoint, workspace, err := execution.ResolveOpenShellGateway(ctx)
	if err != nil {
		return execution.Profile{}, err
	}
	if name != parent.GatewayName || endpoint != parent.GatewayEndpoint || workspace != parent.Workspace {
		return execution.Profile{}, fmt.Errorf("%w: Cursor probe gateway differs from its parent execution authority", execution.ErrIsolationUnverified)
	}
	probe := parent
	probe.SandboxID = ""
	probe.ReadOnly = true
	probe.AllowedPaths = nil
	probe.WriteRoots = nil
	probe.SupervisorDir = registryDir
	pinned, err := execution.PinRuntime(ctx, probe, store.ID(), 1, probeDeadline.UTC().Format(time.RFC3339Nano), registryDir)
	if err != nil {
		return execution.Profile{}, err
	}
	if pinned.GatewayName != parent.GatewayName || pinned.GatewayEndpoint != parent.GatewayEndpoint || pinned.Workspace != parent.Workspace {
		removeErr := discardUnlaunchedCursorIdentity(pinned)
		return execution.Profile{}, errors.Join(fmt.Errorf("%w: Cursor probe gateway changed while pinning its child identity", execution.ErrIsolationUnverified), removeErr)
	}
	if err := execution.RegisterStandaloneRuntime(pinned); err != nil {
		removeErr := discardUnlaunchedCursorIdentity(pinned)
		return execution.Profile{}, errors.Join(err, removeErr)
	}
	pinned.StandaloneRuntime = true
	return pinned, nil
}

func cursorStandaloneRegistryDir(parent execution.Profile) (string, error) {
	lifecycle := filepath.Clean(parent.LifecycleFile)
	if !filepath.IsAbs(lifecycle) || filepath.Base(lifecycle) != "openshell.json" || filepath.Base(filepath.Dir(lifecycle)) != parent.ExecutionID {
		return "", fmt.Errorf("%w: Cursor probe cannot derive a supervisor registry from the parent lifecycle identity", execution.ErrIsolationUnverified)
	}
	scopeDir := filepath.Dir(filepath.Dir(lifecycle))
	scope := filepath.Base(scopeDir)
	supervisorDir := filepath.Dir(scopeDir)
	if (scope != "executions" && scope != "standalone") || filepath.Base(supervisorDir) != "supervisor" {
		return "", fmt.Errorf("%w: parent lifecycle is outside a recognized supervisor execution directory", execution.ErrIsolationUnverified)
	}
	registryDir := filepath.Join(supervisorDir, "standalone")
	if scope == "standalone" {
		registryDir = scopeDir
	}
	if parent.SupervisorDir != "" {
		providedRoot, err := filepath.EvalSymlinks(parent.SupervisorDir)
		if err != nil || providedRoot != registryDir {
			return "", fmt.Errorf("%w: parent supervisor registry conflicts with lifecycle-derived registry", execution.ErrIsolationUnverified)
		}
	}
	resolved, err := filepath.EvalSymlinks(scopeDir)
	if err != nil || resolved != scopeDir {
		return "", fmt.Errorf("%w: parent lifecycle directory is not a canonical supervisor path", execution.ErrIsolationUnverified)
	}
	return registryDir, nil
}

func discardUnlaunchedCursorIdentity(profile execution.Profile) error {
	if !execution.IsOpenShellLauncher(profile.Launcher) || filepath.Base(filepath.Dir(profile.LifecycleFile)) != profile.ExecutionID {
		return fmt.Errorf("%w: refusing to remove an unrecognized Cursor probe identity", execution.ErrIsolationUnverified)
	}
	if err := os.Remove(profile.LifecycleFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: cannot remove unlaunched Cursor probe identity", execution.ErrIsolationUnverified)
	}
	if err := os.Remove(filepath.Dir(profile.LifecycleFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: cannot remove unlaunched Cursor probe directory", execution.ErrIsolationUnverified)
	}
	return nil
}

func cursorNativeToolProbe(ctx context.Context, args []string, profile execution.Profile) ([]byte, error) {
	cmd, err := execution.Command(ctx, args, profile)
	if err != nil {
		if execution.IsOpenShellLauncher(profile.Launcher) {
			if cleanupErr := reconcileCursorProbe(profile); cleanupErr != nil {
				return nil, retainStandaloneProfile(profile, cleanupErr)
			}
			if cleanupErr := completeStandaloneProfile(profile); cleanupErr != nil {
				return nil, retainStandaloneProfile(profile, cleanupErr)
			}
		}
		return nil, errors.New("cursor MCP probe could not start in configured sandbox; check native CLI and sandbox setup")
	}
	output := &cursorCatalogOutput{}
	cmd.Stdout, cmd.Stderr = output, io.Discard
	runErr := process.Run(cmd)
	if execution.IsOpenShellLauncher(profile.Launcher) {
		cleanupErr := reconcileCursorProbe(profile)
		if cleanupErr != nil {
			return nil, retainStandaloneProfile(profile, cleanupErr)
		}
		if cleanupErr = completeStandaloneProfile(profile); cleanupErr != nil {
			return nil, retainStandaloneProfile(profile, cleanupErr)
		}
	}
	if runErr != nil {
		return nil, cursorProbeFailure(ctx)
	}
	if output.overflow {
		return nil, errors.New("cursor MCP tool catalog exceeded supported output limit")
	}
	return output.Bytes(), nil
}

func reconcileCursorProbe(profile execution.Profile) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return execution.ReconcileRuntime(cleanupCtx, profile)
}

func cursorProbeFailure(ctx context.Context) error {
	if ctx.Err() != nil {
		return errors.New("cursor MCP probe cancelled or timed out; reconnect native MCP connection and refresh")
	}
	return errors.New("cursor MCP probe failed; check native MCP authentication, permissions, and sandbox network access")
}

func cursorListToolsCommand(command []string, name string) ([]string, error) {
	index := slices.Index(command, "acp")
	if index < 1 || name == "" {
		return nil, errors.New("cursor native MCP discovery requires an agent acp command")
	}
	return append(slices.Clone(command[:index]), "mcp", "list-tools", name), nil
}

type cursorMCPTransport struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	EnvFile json.RawMessage   `json:"envFile"`
	Auth    json.RawMessage   `json:"auth"`
}

var errCursorMCPTransport = errors.New("unsupported Cursor MCP transport; reconnect using supported stdio, HTTP, or SSE MCP configuration")

func cursorMCPServer(name string, raw json.RawMessage) (map[string]any, error) {
	var config cursorMCPTransport
	if name == "" || json.Unmarshal(raw, &config) != nil {
		return nil, errCursorMCPTransport
	}
	if len(config.EnvFile) > 0 || len(config.Auth) > 0 {
		return nil, errors.New("cursor ACP cannot forward native envFile or auth configuration; reconnect using explicit approved environment or native HTTP headers")
	}
	if config.Command != "" {
		return config.stdio(name)
	}
	return config.remote(name)
}

func (config cursorMCPTransport) stdio(name string) (map[string]any, error) {
	if config.URL != "" || config.Type != "" && config.Type != "stdio" {
		return nil, errCursorMCPTransport
	}
	if config.Args == nil {
		config.Args = []string{}
	}
	return map[string]any{"name": name, "command": config.Command, "args": config.Args, "env": cursorNamedValues(config.Env)}, nil
}

func (config cursorMCPTransport) remote(name string) (map[string]any, error) {
	u, err := url.Parse(config.URL)
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, errCursorMCPTransport
	}
	if config.Type == "" {
		config.Type = "http"
	}
	if config.Type != "http" && config.Type != "sse" {
		return nil, errCursorMCPTransport
	}
	return map[string]any{"name": name, "type": config.Type, "url": config.URL, "headers": cursorNamedValues(config.Headers)}, nil
}

func cursorNamedValues(values map[string]string) []map[string]string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	result := make([]map[string]string, 0, len(names))
	for _, name := range names {
		result = append(result, map[string]string{"name": name, "value": values[name]})
	}
	return result
}

// Cursor CLI versions may emit human-readable output. Only structured native
// annotations establish read permission; unknown tools still require approval.
func cursorToolCatalog(data []byte) ([]model.IntegrationTool, error) {
	type nativeTool struct {
		Name, Description string
		Annotations       map[string]any
	}
	var envelope struct {
		Tools  json.RawMessage `json:"tools"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	data = bytes.TrimSpace(data)
	var raw json.RawMessage
	if len(data) > 0 && data[0] == '[' {
		raw = data
	} else if json.Unmarshal(data, &envelope) == nil {
		if len(envelope.Error) > 0 && !bytes.Equal(envelope.Error, []byte("null")) {
			return nil, errors.New("cursor native MCP reported a tool discovery error; reconnect and refresh")
		}
		if len(envelope.Result) > 0 {
			return cursorToolCatalog(envelope.Result)
		}
		raw = envelope.Tools
	}
	var tools []nativeTool
	if len(raw) == 0 || json.Unmarshal(raw, &tools) != nil {
		return nil, nil
	}
	result := make([]model.IntegrationTool, 0, len(tools))
	for _, tool := range tools {
		if tool.Name != "" {
			result = append(result, model.IntegrationTool{Name: tool.Name, Description: tool.Description, ReadOnly: integrationToolReadOnly(tool.Name, tool.Annotations)})
		}
	}
	return result, nil
}

type cursorCatalogOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *cursorCatalogOutput) Write(p []byte) (int, error) {
	retain := min(len(p), max(0, 1024*1024-b.Len()))
	_, _ = b.Buffer.Write(p[:retain])
	b.overflow = b.overflow || retain < len(p)
	return len(p), nil
}
