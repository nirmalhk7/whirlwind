package workflow

import (
	"reflect"
	"slices"
	"strings"
	"whirlwind/internal/model"
)

func sameDocumentPlan(left, right model.Plan) bool {
	if !slices.Equal([]string{left.Objective, left.What, left.Why, left.Benefit}, []string{right.Objective, right.What, right.Why, right.Benefit}) {
		return false
	}
	if !slices.Equal(left.Where, right.Where) || !slices.Equal(left.AcceptanceCriteria, right.AcceptanceCriteria) {
		return false
	}
	if !reflect.DeepEqual(left.Requirements, right.Requirements) || !reflect.DeepEqual(left.Checks, right.Checks) || !reflect.DeepEqual(left.RepositoryTestCommands, right.RepositoryTestCommands) || !reflect.DeepEqual(left.RepositoryBuildCommands, right.RepositoryBuildCommands) || !reflect.DeepEqual(left.Scope, right.Scope) || !reflect.DeepEqual(left.Exclusions, right.Exclusions) || !reflect.DeepEqual(left.Steps, right.Steps) || left.RepositoryTestInventory != right.RepositoryTestInventory || left.RepositoryBuildInventory != right.RepositoryBuildInventory {
		return false
	}
	return sameDocumentUnits(left.Units, right.Units)
}

func documentRationale(plan model.Plan) (what, why, benefit string) {
	what, why, benefit = strings.TrimSpace(plan.What), strings.TrimSpace(plan.Why), strings.TrimSpace(plan.Benefit)
	if what == "" {
		what = strings.TrimSpace(plan.Objective)
	}
	if why == "" {
		why = strings.TrimSpace(plan.Specification)
	}
	if why == "" {
		why = "Rationale requires review"
	}
	if benefit == "" {
		benefit = strings.Join(plan.AcceptanceCriteria, "\n")
	}
	return
}

func documentPaths(plan model.Plan) []string {
	paths := append([]string{}, plan.Where...)
	if len(paths) == 0 {
		for _, unit := range plan.Units {
			paths = append(paths, unit.AllowedPaths...)
		}
	}
	return paths
}
