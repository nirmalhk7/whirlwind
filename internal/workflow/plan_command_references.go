package workflow

import (
	"fmt"
	"reflect"
	"slices"
	"whirlwind/internal/model"
)

// A document may reference approved commands. It cannot redefine command argv,
// environment, outputs, deadlines or blocking/dependency policy by being saved
// or approved as prose; those changes belong to human project configuration.
func validatePlanCommandReferences(policy model.Policy, plan model.PlanSpecification) error {
	configured := append([]model.Check{}, policy.Checks...)
	for _, rule := range policy.RiskRules {
		configured = append(configured, rule.Checks...)
	}
	byName := map[string]model.Check{}
	for _, check := range configured {
		byName[check.Name] = check
	}
	for _, reference := range plan.Checks {
		approved, ok := byName[reference.Name]
		if !ok || !approvedPlanInvocation(reference, approved) || !approvedPlanCommandPolicy(reference, approved) {
			return planCommandApprovalRequired(reference.Name)
		}
	}
	if err := validateRepositoryPlanCommands(configured, plan); err != nil {
		return err
	}
	// Dependencies of baseline checks must already be resolvable in the human's
	// execution order. Risk-specific dependencies are revalidated after selection.
	return validateConfiguredPlanDependencies(policy.Checks)
}

func planCommandApprovalRequired(name string) error {
	return fmt.Errorf("command %q requires approval through project verification configuration", name)
}

func approvedPlanInvocation(reference, approved model.Check) bool {
	approvedMode := approved.Mode
	if approvedMode == "" {
		approvedMode = "verify"
	}
	if reference.Mode != "" && reference.Mode != approvedMode {
		return false
	}
	if len(reference.Command) > 0 && !slices.Equal(reference.Command, approved.Command) {
		return false
	}
	if reference.TimeoutSeconds != 0 && reference.TimeoutSeconds != approved.TimeoutSeconds {
		return false
	}
	if len(reference.OutputPaths) > 0 && !slices.Equal(reference.OutputPaths, approved.OutputPaths) {
		return false
	}

	return true
}

func approvedPlanCommandPolicy(reference, approved model.Check) bool {
	if len(reference.DependsOn) > 0 && !slices.Equal(reference.DependsOn, approved.DependsOn) {
		return false
	}
	if reference.Required != nil && (approved.Required == nil && !*reference.Required || approved.Required != nil && *reference.Required != *approved.Required) {
		return false
	}
	if reference.ReportFormat != "" && reference.ReportFormat != approved.ReportFormat {
		return false
	}
	if reference.Environment != nil && !reflect.DeepEqual(reference.Environment, approved.Environment) {
		return false
	}
	return true
}

func validateRepositoryPlanCommands(configured []model.Check, plan model.PlanSpecification) error {
	for _, command := range append(append([][]string{}, plan.RepositoryTestCommands...), plan.RepositoryBuildCommands...) {
		found := false
		for _, approved := range configured {
			if slices.Equal(command, approved.Command) {
				found = true
				break
			}
		}
		if !found {
			return planCommandApprovalRequired(fmt.Sprint(command))
		}
	}
	return nil
}

func validateConfiguredPlanDependencies(checks []model.Check) error {
	seen := map[string]bool{}
	for _, check := range checks {
		if seen[check.Name] {
			return fmt.Errorf("duplicate command ID %q in verification configuration", check.Name)
		}
		for _, dependency := range check.DependsOn {
			if !seen[dependency] {
				return fmt.Errorf("verification configuration: command %s dependency %s must precede it", check.Name, dependency)
			}
		}
		seen[check.Name] = true
	}
	return nil
}
