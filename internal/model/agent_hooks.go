package model

import (
	"errors"
	"fmt"
	"slices"
)

func ValidateChecks(checks []Check) error {
	names := map[string]bool{}
	for _, c := range checks {
		if !slices.Contains([]string{"", "verify", "autofix"}, c.Mode) {
			return errors.New("verification command mode must be verify or autofix")
		}
		if c.FreshnessSeconds < 0 || (c.Reusable && c.FreshnessSeconds == 0) {
			return errors.New("reusable checks require positive freshness_seconds")
		}
		if c.Name == "" || len(c.Command) == 0 || names[c.Name] {
			return errors.New("checks need unique names and command arrays")
		}
		names[c.Name] = true
	}
	return nil
}

func ValidateAgentHooks(config map[string]AgentHookChecks) error {
	for role, hooks := range config {
		if !slices.Contains(AgentRoles(), role) {
			return fmt.Errorf("unknown agent hook role %q", role)
		}
		if _, err := ResolveAgentHooks(hooks); err != nil {
			return fmt.Errorf("agent hooks for %s: %w", role, err)
		}
	}
	return nil
}

// ResolveAgentHooks orders approved settings commands by their phase dependencies.
func ResolveAgentHooks(hooks AgentHookChecks) (AgentHookChecks, error) {
	before, err := resolveHookChecks(hooks.Before)
	if err != nil {
		return AgentHookChecks{}, fmt.Errorf("before: %w", err)
	}
	after, err := resolveHookChecks(hooks.After)
	if err != nil {
		return AgentHookChecks{}, fmt.Errorf("after: %w", err)
	}
	return AgentHookChecks{Before: before, After: after}, nil
}

func resolveHookChecks(configured []Check) ([]Check, error) {
	if err := ValidateChecks(configured); err != nil {
		return nil, err
	}
	byName := make(map[string]Check, len(configured))
	for _, check := range configured {
		if check.Mode == "autofix" {
			return nil, fmt.Errorf("mutating check %q cannot run as an agent hook", check.Name)
		}
		byName[check.Name] = check
	}
	order := hookDependencyOrder{byName: byName, visiting: map[string]bool{}, resolved: map[string]bool{}}
	for _, check := range configured {
		if err := order.visit(check.Name); err != nil {
			return nil, err
		}
	}
	return order.checks, nil
}

type hookDependencyOrder struct {
	byName             map[string]Check
	visiting, resolved map[string]bool
	checks             []Check
}

func (order *hookDependencyOrder) visit(id string) error {
	if order.visiting[id] {
		return fmt.Errorf("agent hook check dependency cycle at %q", id)
	}
	if order.resolved[id] {
		return nil
	}
	check, exists := order.byName[id]
	if !exists {
		return fmt.Errorf("unknown hook check %q", id)
	}
	order.visiting[id] = true
	for _, dependency := range check.DependsOn {
		if err := order.visit(dependency); err != nil {
			return err
		}
	}
	delete(order.visiting, id)
	order.resolved[id] = true
	order.checks = append(order.checks, check)
	return nil
}
