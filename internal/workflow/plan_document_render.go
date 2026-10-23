package workflow

import (
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
