package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ModelCatalog struct {
	Models          []ModelInfo      `json:"models"`
	Current         string           `json:"current,omitempty"`
	ThinkingOptions []ThinkingOption `json:"thinking_options"`
}

type ThinkingOption struct {
	ConfigID string      `json:"config_id"`
	Name     string      `json:"name"`
	Current  string      `json:"current"`
	Options  []ModelInfo `json:"options"`
}

type acpOptionValue struct {
	Value   string           `json:"value"`
	Name    string           `json:"name"`
	Options []acpOptionValue `json:"options"`
}
type acpConfigOption struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Category string           `json:"category"`
	Type     string           `json:"type"`
	Current  json.RawMessage  `json:"currentValue"`
	Options  []acpOptionValue `json:"options"`
}
type acpSession struct {
	ID    string `json:"sessionId"`
	Modes struct {
		Available []struct {
			ID string `json:"id"`
		} `json:"availableModes"`
	} `json:"modes"`
	Models struct {
		Current   string `json:"currentModelId"`
		Available []struct {
			ID   string `json:"modelId"`
			Name string `json:"name"`
		} `json:"availableModels"`
	} `json:"models"`
	ConfigOptions []acpConfigOption `json:"configOptions"`
}

func (s acpSession) modelCatalog() (ModelCatalog, string) {
	catalog := ModelCatalog{Models: []ModelInfo{}}
	for _, option := range s.ConfigOptions {
		if option.Type == "select" && (option.Category == "model" || option.ID == "model") {
			_ = json.Unmarshal(option.Current, &catalog.Current)
			catalog.Models = append(catalog.Models, optionValues(option.Options)...)
			return catalog, option.ID
		}
	}
	catalog.Current = s.Models.Current
	for _, value := range s.Models.Available {
		catalog.Models = append(catalog.Models, ModelInfo{value.ID, value.Name})
	}
	return catalog, ""
}

// ACPCommand uses the same maintained adapter for discovery and explicit thinking execution.
func ACPCommand(p model.Provider) []string {
	if p.Kind == "acp" {
		return p.Command
	}
	if len(p.ModelCommand) > 0 {
		return p.ModelCommand
	}
	adapter := "@agentclientprotocol/codex-acp@2.2.2"
	if p.Kind == "claude" {
		adapter = "@agentclientprotocol/claude-agent-acp@0.90.0"
	}
	return []string{"npx", "--yes", adapter}
}

func ExecutionProvider(p model.Provider, thinking *model.ThinkingChoice) model.Provider {
	if !p.NativeIntegrations && thinking != nil && (p.Kind == "codex" || p.Kind == "claude") {
		p.Command = ACPCommand(p)
		p.Kind = "acp"
	}
	return p
}

// Models asks the installed source for model IDs without running an inference turn.
func Models(ctx context.Context, p model.Provider, cwd string, selectedModels ...string) (ModelCatalog, error) {
	catalog := ModelCatalog{Models: []ModelInfo{}}
	if p.Kind != "codex" && p.Kind != "claude" && p.Kind != "acp" {
		return catalog, fmt.Errorf("unsupported model source: %s", p.Kind)
	}
	command := ACPCommand(p)
	selectedModel := ""
	if len(selectedModels) > 0 {
		selectedModel = selectedModels[0]
	}
	profile, err := execution.ForProvider(cwd, true, "", p.Sandbox, command)
	if err != nil {
		return catalog, err
	}
	cwd = profile.CWD
	s := &providerSession{ctx: ctx, input: Input{CWD: cwd, ReadOnly: true, Model: selectedModel}, client: NewClient(Input{CWD: cwd, ReadOnly: true, Execution: &profile}, Hooks{})}
	defer s.client.Close()
	rpc, err := StartWithProfile(ctx, command, profile, s.handle)
	if err != nil {
		return catalog, err
	}
	s.rpc = rpc
	defer rpc.Close()
	return s.discoverModelCatalog(cwd)
}

func (s *providerSession) discoverModelCatalog(cwd string) (ModelCatalog, error) {
	catalog := ModelCatalog{Models: []ModelInfo{}}
	if _, err := s.initializeACP(); err != nil {
		return catalog, err
	}
	var session acpSession
	if err := s.rpc.Call(s.ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, &session); err != nil {
		return catalog, err
	}
	if err := s.setACPModel(&session); err != nil {
		return catalog, err
	}
	catalog, _ = session.modelCatalog()
	catalog.ThinkingOptions = session.thinkingOptions()
	return catalog, nil
}
