package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
)

func sameDocumentUnits(left, right []model.Unit) bool {
	if len(left) != len(right) {
		return false
	}
	for i, unit := range left {
		other := right[i]
		if unit.ID != other.ID || unit.Objective != other.Objective || !slices.Equal(unit.Dependencies, other.Dependencies) || !slices.Equal(unit.AllowedPaths, other.AllowedPaths) {
			return false
		}
	}
	return true
}

// Unit scopes remain visible prose in the approved document. A manually written
// implementation without unit headings executes as one unit with the full scope.
func parseDocumentUnits(implementation string, where []string) ([]model.Unit, error) {
	if !strings.HasPrefix(strings.TrimSpace(implementation), "### ") {
		return []model.Unit{{ID: "main", Objective: implementation, AllowedPaths: append([]string{}, where...)}}, nil
	}
	var units []model.Unit
	for _, section := range strings.Split(strings.TrimSpace(implementation), "\n### ") {
		unit, err := parseDocumentUnit(strings.TrimPrefix(section, "### "))
		if err != nil {
			return nil, err
		}
		units = append(units, unit)
	}
	if err := ValidateUnits(units); err != nil {
		return nil, err
	}
	return units, validateDocumentUnitScopes(units, where)
}

func parseDocumentUnit(section string) (model.Unit, error) {
	lines := strings.Split(section, "\n")
	unit := model.Unit{ID: strings.TrimSpace(lines[0])}
	var objective []string
	metadata := map[string]*[]string{"Dependencies:": &unit.Dependencies, "Allowed paths:": &unit.AllowedPaths}
	seen := map[string]bool{}
	for _, line := range lines[1:] {
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		target := metadata[key+":"]
		if !found || target == nil {
			objective = append(objective, line)
			continue
		}
		if seen[key] || json.Unmarshal([]byte(strings.TrimSpace(value)), target) != nil {
			return unit, fmt.Errorf("unit %s requires one JSON string array for %s", unit.ID, key)
		}
		seen[key] = true
	}
	unit.Objective = strings.TrimSpace(strings.Join(objective, "\n"))
	if unit.Objective == "" || len(unit.AllowedPaths) == 0 {
		return unit, errors.New("each implementation unit requires an objective and explicit allowed paths")
	}
	return unit, nil
}

func validateDocumentUnitScopes(units []model.Unit, where []string) error {
	covered := map[string]bool{}
	for _, unit := range units {
		for _, path := range unit.AllowedPaths {
			if !coverDocumentScope(path, where, covered) {
				return fmt.Errorf("unit %s path %s is absent from plan Where", unit.ID, path)
			}
		}
	}
	if len(covered) != len(where) {
		return errors.New("implementation units must cover every plan Where path")
	}
	return nil
}

func coverDocumentScope(path string, where []string, covered map[string]bool) bool {
	matched := false
	for _, scope := range where {
		if policy.Match(scope, path) || strings.HasPrefix(path, strings.TrimSuffix(scope, "/")+"/") {
			covered[scope] = true
			matched = true
		}
	}
	return matched
}
