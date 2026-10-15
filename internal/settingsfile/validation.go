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
	capable := ok && p.Kind != "unavailable" && slices.Contains(p.Capabilities, role)
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
