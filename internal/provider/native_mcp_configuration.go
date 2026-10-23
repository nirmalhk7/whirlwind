package provider

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"whirlwind/internal/model"
)

const (
	nativeClaudeUserConfig = ".claude.json"
	nativeClaudeSettings   = ".claude/settings.json"
	nativeCursorMCPConfig  = ".cursor/mcp.json"
	nativeProjectMCPConfig = ".mcp.json"
)

type nativeMCPConfig struct {
	Servers  map[string]json.RawMessage `json:"mcpServers"`
	Disabled []string                   `json:"disabledMcpServers"`
	Rejected []string                   `json:"disabledMcpjsonServers"`
	Projects map[string]nativeMCPConfig `json:"projects"`
}

type nativeMCPLoader struct{ configs []nativeMCPConfig }

// NativeMCPDefinitions loads native JSON transports without logging credentials.
// Provider runtime discovery remains authoritative for OAuth/cloud connectors.
func NativeMCPDefinitions(p model.Provider, cwd string, approvedEnvironment ...[]string) (map[string]json.RawMessage, error) {
	family, err := nativeProviderFamily(p)
	if err != nil {
		return nil, err
	}
	if family == "codex" {
		return map[string]json.RawMessage{}, nil // Codex owns its TOML configuration and discovery.
	}
	configs, err := loadNativeMCPScopes(family, cwd)
	if err != nil {
		return nil, err
	}
	definitions, err := mergeNativeMCPDefinitions(configs)
	if err != nil {
		return nil, err
	}
	return resolveNativeMCPDefinitions(definitions, family, p.Sandbox, approvedEnvironment)
}

func loadNativeMCPScopes(family, cwd string) ([]nativeMCPConfig, error) {
	home, err := nativeUserHome()
	if err != nil {
		return nil, err
	}
	loader := nativeMCPLoader{}
	files := []string{nativeClaudeUserConfig, nativeClaudeSettings}
	if family == "cursor" {
		files = []string{nativeCursorMCPConfig}
	}
	if err := loader.loadFiles(home, files); err != nil {
		return nil, err
	}
	if cwd == "" {
		return loader.configs, nil
	}
	project, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return nil, errors.New("native MCP project directory unavailable")
	}
	err = loader.loadProject(family, project)
	return loader.configs, err
}

func (loader *nativeMCPLoader) loadFiles(root string, files []string) error {
	for _, relative := range files {
		if err := loader.load(root, relative); err != nil {
			return err
		}
	}
	return nil
}

func (loader *nativeMCPLoader) load(root, relative string) error {
	path, exists, err := nativeRegularFile(root, relative)
	if err != nil || !exists {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("native MCP configuration could not be read")
	}
	var config nativeMCPConfig
	if json.Unmarshal(data, &config) != nil {
		return errors.New("native MCP configuration contains invalid JSON or unsupported fields")
	}
	loader.configs = append(loader.configs, config)
	return nil
}

func (loader *nativeMCPLoader) loadProject(family, project string) error {
	if family == "cursor" {
		return loader.load(project, nativeCursorMCPConfig)
	}
	// Claude's local-scope MCP definitions live in its native user file.
	for _, config := range slices.Clone(loader.configs) {
		if local, found := config.Projects[project]; found {
			loader.configs = append(loader.configs, local)
		}
	}
	return loader.loadFiles(project, []string{nativeProjectMCPConfig, nativeClaudeSettings, ".claude/settings.local.json"})
}

func mergeNativeMCPDefinitions(configs []nativeMCPConfig) (map[string]json.RawMessage, error) {
	definitions := map[string]json.RawMessage{}
	disabled := map[string]bool{}
	for _, config := range configs {
		if err := config.apply(definitions); err != nil {
			return nil, err
		}
		for _, name := range append(slices.Clone(config.Disabled), config.Rejected...) {
			disabled[name] = true
		}
	}
	for name := range disabled {
		delete(definitions, name)
	}
	return definitions, nil
}

func (config nativeMCPConfig) apply(definitions map[string]json.RawMessage) error {
	for name, raw := range config.Servers {
		enabled, err := nativeMCPEnabled(raw)
		if err != nil {
			return err
		}
		if !enabled {
			delete(definitions, name)
			continue
		}
		definitions[name] = slices.Clone(raw)
	}
	return nil
}

func nativeMCPEnabled(raw json.RawMessage) (bool, error) {
	var state struct {
		Disabled bool  `json:"disabled"`
		Enabled  *bool `json:"enabled"`
	}
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &state) != nil {
		return false, errors.New("native MCP server definition must be a JSON object")
	}
	return !state.Disabled && (state.Enabled == nil || *state.Enabled), nil
}

func resolveNativeMCPDefinitions(definitions map[string]json.RawMessage, family string, policy *model.SandboxPolicy, approvedEnvironment [][]string) (map[string]json.RawMessage, error) {
	if family != "cursor" {
		return definitions, nil
	}
	if len(approvedEnvironment) > 0 {
		policy = &model.SandboxPolicy{Environment: approvedEnvironment[0]}
	}
	for name, raw := range definitions {
		resolved, err := resolveCursorMCPDefinition(raw, policy)
		if err != nil {
			return nil, err
		}
		definitions[name] = resolved
	}
	return definitions, nil
}
