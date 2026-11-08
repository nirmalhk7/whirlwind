package provider

import (
	"context"
	"encoding/json"
	"errors"

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
		if len(p.Command) == 0 && p.ACPProfile != "" {
			if profile, ok := ACPProfileByID(p.ACPProfile); ok {
				return profile.Command
			}
		}
		return p.Command
	}
	return nil
}

func ExecutionProvider(p model.Provider, thinking *model.ThinkingChoice) model.Provider {
	_ = thinking
	resolved, err := ResolveExecutionProvider(p)
	if err != nil {
		p.Command = nil
		p.ModelCommand = nil
		return p
	}
	return resolved
}

// Models asks the installed source for model IDs without running an inference turn.
func Models(ctx context.Context, p model.Provider, cwd string, selectedModels ...string) (result ModelCatalog, err error) {
	supervisorDir, err := defaultStandaloneSupervisorDir()
	if err != nil {
		return ModelCatalog{Models: []ModelInfo{}}, err
	}
	return ModelsWithSupervisorDir(ctx, p, cwd, supervisorDir, selectedModels...)
}

// ModelsWithSupervisorDir performs metadata discovery with a durable standalone runtime locator.
func ModelsWithSupervisorDir(ctx context.Context, p model.Provider, cwd, supervisorDir string, selectedModels ...string) (result ModelCatalog, err error) {
	catalog := ModelCatalog{Models: []ModelInfo{}}
	resolved, resolveErr := ResolveExecutionProvider(p)
	if resolveErr != nil {
		return catalog, resolveErr
	}
	p = resolved
	command := ACPCommand(p)
	selectedModel := ""
	if len(selectedModels) > 0 {
		selectedModel = selectedModels[0]
	}
	profile, err := execution.ForProvider(cwd, true, "", p.Sandbox, command)
	if err != nil {
		return catalog, err
	}
	profile, err = pinStandaloneProfile(ctx, profile, supervisorDir)
	if err != nil {
		return catalog, err
	}
	standalone := profile.StandaloneRuntime
	transferred := false
	defer func() {
		if standalone && !transferred {
			err = errors.Join(err, completeStandaloneProfile(profile))
		}
	}()
	cwd = profile.CWD
	input := Input{ProviderID: p.ID, ACPProfile: p.ACPProfile, CWD: cwd, ReadOnly: true, Model: selectedModel, Execution: &profile}
	s := &providerSession{ctx: ctx, input: input, client: NewClient(input, Hooks{})}
	defer func() { err = errors.Join(err, s.client.Close()) }()
	cleanup, err := s.prepareProviderIntegrations(p, p, &profile)
	if err != nil {
		return catalog, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	rpc, err := StartWithProfile(ctx, command, profile, s.handle)
	if err != nil {
		return catalog, actionableACPError(p, err)
	}
	transferred = true
	s.rpc = rpc
	defer func() { err = errors.Join(err, rpc.Close()) }()
	catalog, err = s.discoverModelCatalog(cwd)
	return catalog, authenticationACPError(p, err)
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
