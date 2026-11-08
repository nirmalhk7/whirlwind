package workflow

import (
	"fmt"
	"path/filepath"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
)

// ValidateUnitScopes checks all scopes before publication or workspace side effects.
func ValidateUnitScopes(p model.Project, units []model.Unit) error {
	for _, unit := range units {
		for _, path := range unit.AllowedPaths {
			if err := validateUnitPath(p.Policy, path); err != nil {
				return fmt.Errorf("unit %s scope %q: %w; use explicit permitted files or a supported directory/** scope", unit.ID, path, err)
			}
		}
	}
	return nil
}

func validateUnitPath(p model.Policy, path string) error {
	if path == "" || strings.HasSuffix(path, "/") {
		return fmt.Errorf("directory scope requires /**")
	}
	base := strings.TrimSuffix(path, "/**")
	if path != "**" && strings.Contains(base, "**") {
		return fmt.Errorf("recursive glob is supported only as directory/**")
	}
	if _, err := filepath.Match(path, "scope-probe"); err != nil {
		return fmt.Errorf("invalid glob: %w", err)
	}
	if !policy.AllowedPath(p, path) || !unitScopeContained(p, path) {
		return fmt.Errorf("scope exceeds project policy")
	}
	return nil
}

// Wildcards cannot be proven narrower by matching their literal spelling.
// Accept equal patterns or a containing recursive directory; otherwise require files.
func unitScopeContained(p model.Policy, path string) bool {
	if !strings.ContainsAny(path, "*?[") {
		return true
	}
	for _, scope := range p.AllowedPaths {
		if scope == path || scope == "**" || (strings.HasSuffix(scope, "/**") && strings.HasPrefix(path, strings.TrimSuffix(scope, "**"))) {
			return true
		}
	}
	return false
}
