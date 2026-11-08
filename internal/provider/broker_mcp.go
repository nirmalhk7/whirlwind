package provider

import (
	"errors"
	"os"
	"path/filepath"
	"slices"

	"whirlwind/internal/execution"
)

const selectedBrokerServerName = "whirlwind_selected_read_tools"

func (s *providerSession) prepareBrokerMCP(profile *execution.Profile) error {
	if profile == nil || profile.MCPBroker == nil {
		return errors.New("selected MCP broker execution profile required")
	}
	if !execution.IsOpenShellLauncher(profile.Launcher) {
		return errors.New("selected MCP broker requires the OpenShell host proxy and Linux guest shim")
	}
	script := filepath.Join(filepath.Dir(profile.Launcher), "mcp-stdio.mjs")
	info, err := os.Lstat(script)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("selected MCP broker guest shim is unavailable; restore bridges/openshell/mcp-stdio.mjs")
	}
	script, err = filepath.EvalSymlinks(script)
	if err != nil {
		return errors.New("selected MCP broker guest shim path could not be resolved")
	}
	if !slices.Contains(profile.ReadRoots, script) {
		profile.ReadRoots = append(profile.ReadRoots, script)
		slices.Sort(profile.ReadRoots)
	}
	s.input.Execution = profile
	return nil
}

func (s *providerSession) brokerMCPServer() map[string]any {
	if s.input.MCPBroker == nil || s.input.Execution == nil {
		return nil
	}
	script := filepath.Join(filepath.Dir(s.input.Execution.Launcher), "mcp-stdio.mjs")
	return map[string]any{"name": selectedBrokerServerName, "command": "node", "args": []string{script, s.input.MCPBroker.ID}, "env": []any{}}
}
