package workflow

import (
	"errors"
	"fmt"
	"strings"

	"whirlwind/internal/model"
)

func planVerificationChecks(policy model.Policy, plan model.PlanSpecification) ([]model.Check, error) {
	checks := append([]model.Check{}, policy.Checks...)
	seen := map[string]bool{}
	for _, check := range checks {
		seen[check.Name] = true
	}
	for _, check := range plan.Checks {
		if err := appendApprovedPlanCheck(&checks, seen, check); err != nil {
			return nil, err
		}
	}
	if err := appendRepositoryCommands(&checks, seen, "test", plan.RepositoryTestCommands); err != nil {
		return nil, err
	}
	if err := appendRepositoryCommands(&checks, seen, "build", plan.RepositoryBuildCommands); err != nil {
		return nil, err
	}
	return checks, nil
}

func appendApprovedPlanCheck(checks *[]model.Check, seen map[string]bool, check model.Check) error {
	if strings.TrimSpace(check.Name) == "" || len(check.Command) == 0 || strings.TrimSpace(check.Command[0]) == "" {
		return errors.New("each approved plan check needs a name and command")
	}
	if seen[check.Name] {
		return fmt.Errorf("approved plan check duplicates configured check name %q", check.Name)
	}
	seen[check.Name] = true
	*checks = append(*checks, check)
	return nil
}

func appendRepositoryCommands(checks *[]model.Check, seen map[string]bool, kind string, commands [][]string) error {
	for index, command := range commands {
		if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
			return fmt.Errorf("approved repository %s command is empty", kind)
		}
		name := fmt.Sprintf("plan-%s-%d", kind, index+1)
		if seen[name] {
			return fmt.Errorf("generated repository %s check duplicates check name %q", kind, name)
		}
		seen[name] = true
		*checks = append(*checks, model.Check{Name: name, Command: append([]string{}, command...)})
	}
	return nil
}
