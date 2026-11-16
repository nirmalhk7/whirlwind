package workflow

import (
	"fmt"
	"whirlwind/internal/model"
)

// Select only the requested check and its prerequisites, retaining the human's
// command order. The runner validates the resulting dependency graph again.
func brokerCheckCommands(policy model.Policy, id string) ([]model.Check, error) {
	needed, visiting := map[string]bool{}, map[string]bool{}

	if err := visitBrokerCheck(policy, id, needed, visiting); err != nil {
		return nil, err
	}
	configured := append([]model.Check{}, policy.Checks...)
	for _, rule := range policy.RiskRules {
		configured = append(configured, rule.Checks...)
	}
	var selected []model.Check
	seen := map[string]bool{}
	for _, check := range configured {
		if !needed[check.Name] || seen[check.Name] {
			continue
		}
		for _, dependency := range check.DependsOn {
			if !seen[dependency] {
				return nil, fmt.Errorf("command %s dependency %s must precede it in approved configuration", check.Name, dependency)
			}
		}
		selected = append(selected, check)
		seen[check.Name] = true
	}
	return selected, nil
}

func visitBrokerCheck(policy model.Policy, id string, needed, visiting map[string]bool) error {
	if visiting[id] {
		return fmt.Errorf("verification dependency cycle at %s", id)
	}
	if needed[id] {
		return nil
	}
	check, err := committedCheck(policy, id)
	if err != nil {
		return err
	}
	visiting[id] = true
	for _, dependency := range check.DependsOn {
		if err := visitBrokerCheck(policy, dependency, needed, visiting); err != nil {
			return err
		}
	}
	delete(visiting, id)
	needed[id] = true
	return nil
}
