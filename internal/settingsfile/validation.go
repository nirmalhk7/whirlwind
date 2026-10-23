package settingsfile

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/workflow"
)

func validateWorkers(s workflow.Settings) error {
	capacity := s.GlobalSlots >= 1 && s.GlobalSlots <= 32 && s.ProjectSlots >= 1 && s.ProjectSlots <= 32
	interval := s.ReconciliationSeconds >= 30 && s.ReconciliationSeconds <= 86400
	if !capacity || !interval {
		return errors.New("invalid worker capacity or reconciliation interval in settings.yaml")
	}
	if s.ProviderStatusCheckSeconds < 1 || s.ProviderStatusCheckSeconds > 86400 {
		return errors.New("provider_status_check_seconds must be 1..86400 in settings.yaml")
	}
	if err := s.Admission.Validate(); err != nil {
		return err
	}
	return s.Ideation.Validate()
}

func validateProviders(values []Provider) (map[string]Provider, error) {
	providers := map[string]Provider{}
	for _, p := range values {
		if p.ID == "" || p.Name == "" {
			return nil, errors.New("providers need IDs and names")
		}
		if _, ok := providers[p.ID]; ok {
			return nil, fmt.Errorf("duplicate provider %s", p.ID)
		}
		if err := validateProviderCommand(p); err != nil {
			return nil, err
		}
		providers[p.ID] = p
	}
	return providers, nil
}

func validateIntegrations(settings model.IntegrationSettings, providers map[string]Provider) error {
	validTarget := func(provider, ref string) error {
		p, ok := providers[provider]
		if !ok || p.Kind == "unavailable" {
			return fmt.Errorf("integration references unconfigured provider %q", provider)
		}
		if strings.TrimSpace(ref) == "" || ref != strings.TrimSpace(ref) {
			return errors.New("integration needs an exact native ref")
		}
		return nil
	}
	seen := map[[2]string]bool{}
	for _, source := range settings.Sources {
		if err := validTarget(source.Provider, source.Ref); err != nil {
			return err
		}
		key := [2]string{source.Provider, source.Ref}
		if seen[key] {
			return errors.New("duplicate integration scope override")
		}
		seen[key] = true
		for _, scope := range source.Scopes {
			if strings.TrimSpace(scope) == "" {
				return errors.New("integration scopes cannot be empty")
			}
		}
	}
	for repository, target := range settings.Publication {
		canonical, err := workflow.CanonicalRemote(repository)
		if err != nil || canonical != repository {
			return fmt.Errorf("integration publication key must be canonical HTTPS repository URL: %q", repository)
		}
		if err := validTarget(target.Provider, target.Ref); err != nil {
			return err
		}
	}
	return nil
}

func validateProviderCommand(p Provider) error {
	switch p.Kind {
	case "acp", "codex", "claude":
		if len(p.Command) == 0 {
			return fmt.Errorf("provider %s needs command", p.ID)
		}
	case "unavailable":
	default:
		return fmt.Errorf("unknown provider kind %s", p.Kind)
	}
	return nil
}

func validateChain(role string, chain []model.AgentChoice, providers map[string]Provider) error {
	if !slices.Contains(model.AgentRoles(), role) {
		return fmt.Errorf("unknown agent role %s", role)
	}
	if len(chain) == 0 {
		return fmt.Errorf("agent %s needs a primary choice; omit role to use defaults", role)
	}
	seen := map[[2]string]bool{}
	for _, choice := range chain {
		key := [2]string{choice.Provider, choice.Model}
		if err := validateChoice(role, choice, providers, seen[key]); err != nil {
			return err
		}
		seen[key] = true
	}
	return nil
}

func validateChoice(role string, choice model.AgentChoice, providers map[string]Provider, duplicate bool) error {
	p, ok := providers[choice.Provider]
	capable := ok && p.Kind != "unavailable" && (slices.Contains(p.Capabilities, role) || role == "submit" && slices.Contains(p.Capabilities, "code"))
	if !capable || strings.TrimSpace(choice.Model) == "" || duplicate {
		return fmt.Errorf("invalid provider/model choice for %s", role)
	}
	if choice.Thinking != nil {
		if strings.TrimSpace(choice.Thinking.ConfigID) == "" || strings.TrimSpace(choice.Thinking.Value) == "" {
			return errors.New("thinking needs config_id and value")
		}
	}
	return nil
}
