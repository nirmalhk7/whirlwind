package provider

import (
	"fmt"
	"strings"
	"whirlwind/internal/model"
)

func integrationToolReadOnly(_ string, annotations map[string]any) bool {
	read, _ := annotations["readOnlyHint"].(bool)
	if !read {
		read, _ = annotations["readOnly"].(bool)
	}
	destructive, _ := annotations["destructiveHint"].(bool)
	if !destructive {
		destructive, _ = annotations["destructive"].(bool)
	}
	return read && !destructive
}
func (s *providerSession) checkIntegrationInventory(catalog []model.Integration) error {
	byRef := map[string][]model.Integration{}
	for _, item := range catalog {
		byRef[item.Ref] = append(byRef[item.Ref], item)
	}
	for _, target := range s.input.RequiredIntegrations {
		if err := s.validateRequiredIntegration(target, byRef[target.Ref]); err != nil {
			return err
		}
	}
	if !s.input.DiscoverConnected {
		return nil
	}
	return validateConnectedIntegrations(catalog)
}

func (s *providerSession) validateRequiredIntegration(target model.IntegrationTarget, matches []model.Integration) error {
	if target.Provider != s.input.ProviderID {
		return fmt.Errorf("integration provider mismatch: %s requires %s, selected %s", target.Ref, target.Provider, s.input.ProviderID)
	}
	if len(matches) == 0 {
		return fmt.Errorf("integration %s unavailable in %s local API; reconnect using supported MCP configuration", target.Ref, s.input.ProviderID)
	}
	for _, item := range matches {
		if !item.Enabled || !item.Callable {
			return fmt.Errorf("integration %s is not callable: %s; reconnect in %s and refresh", item.Ref, item.Error, s.input.ProviderID)
		}
	}
	return nil
}

func validateConnectedIntegrations(catalog []model.Integration) error {
	for _, item := range catalog {
		if item.Enabled && !item.Callable {
			return fmt.Errorf("connected integration %s is unavailable: %s; reconnect and refresh", item.Ref, item.Error)
		}
	}
	return nil
}

func (s *providerSession) integrationInputs(catalog []model.Integration) []any {
	inputs := []any{map[string]any{"type": "text", "text": s.input.Prompt}}
	for _, item := range catalog {
		if !item.Enabled || !item.Callable || item.Kind != "app" {
			continue
		}
		wanted := s.input.DiscoverConnected
		for _, r := range s.input.RequiredIntegrations {
			wanted = wanted || r.Ref == item.Ref
		}
		if wanted {
			inputs = append(inputs, map[string]any{"type": "mention", "name": item.Name, "path": item.Ref})
		}
	}
	return inputs
}
func IntegrationRefName(ref string) string { return strings.TrimPrefix(ref, "mcp://") }
