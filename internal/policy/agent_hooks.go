package policy

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"whirlwind/internal/model"
)

func ValidateAgentHooks(config map[string]model.AgentHookChecks) error {
	for role, hooks := range config {
		if !slices.Contains(model.AgentRoles(), role) {
			return fmt.Errorf("unknown agent hook role %q", role)
		}
		if _, err := ResolveAgentHooks(hooks); err != nil {
			return fmt.Errorf("agent hooks for %s: %w", role, err)
		}
	}
	return nil
}

// ResolveAgentHooks orders approved settings commands by their phase dependencies.
func ResolveAgentHooks(hooks model.AgentHookChecks) (model.AgentHookChecks, error) {
	before, err := resolveHookChecks(hooks.Before)
	if err != nil {
		return model.AgentHookChecks{}, fmt.Errorf("before: %w", err)
	}
	after, err := resolveHookChecks(hooks.After)
	if err != nil {
		return model.AgentHookChecks{}, fmt.Errorf("after: %w", err)
	}
	return model.AgentHookChecks{Before: before, After: after}, nil
}

func resolveHookChecks(configured []model.Check) ([]model.Check, error) {
	if err := validateChecks(configured); err != nil {
		return nil, err
	}
	byName := make(map[string]model.Check, len(configured))
	for _, check := range configured {
		if check.Mode == "autofix" {
			return nil, fmt.Errorf("mutating check %q cannot run as an agent hook", check.Name)
		}
		byName[check.Name] = check
	}
	var checks []model.Check
	visiting, resolved := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("agent hook check dependency cycle at %q", id)
		}
		if resolved[id] {
			return nil
		}
		check, exists := byName[id]
		if !exists {
			return fmt.Errorf("unknown hook check %q", id)
		}
		visiting[id] = true
		for _, dependency := range check.DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, id)
		resolved[id] = true
		checks = append(checks, check)
		return nil
	}
	for _, check := range configured {
		if err := visit(check.Name); err != nil {
			return nil, err
		}
	}
	return checks, nil
}

// CommittedCheck resolves an exact check ID across project and risk checks.
func CommittedCheck(policy model.Policy, id string) (model.Check, error) {
	if id == "" {
		return model.Check{}, errors.New("committed check ID required")
	}
	checks := append([]model.Check{}, policy.Checks...)
	for _, rule := range policy.RiskRules {
		checks = append(checks, rule.Checks...)
	}
	var found *model.Check
	for _, check := range checks {
		if check.Name != id {
			continue
		}
		if found != nil && !reflect.DeepEqual(*found, check) {
			return model.Check{}, errors.New("ambiguous committed check ID")
		}
		copy := check
		found = &copy
	}
	if found == nil {
		return model.Check{}, fmt.Errorf("unknown committed check ID: %s", id)
	}
	return *found, nil
}
