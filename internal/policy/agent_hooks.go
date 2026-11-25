package policy

import (
	"errors"
	"fmt"
	"reflect"

	"whirlwind/internal/model"
)

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
