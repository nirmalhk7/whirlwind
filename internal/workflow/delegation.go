package workflow

import (
	"strings"
	"whirlwind/internal/model"
)

// selectUnitBatch is deliberately conservative: unknown scope and wildcard
// prefixes that cannot be proven disjoint are serialized.
func selectUnitBatch(units []model.Unit, ready []int, limit int, parallel bool) []int {
	if len(ready) == 0 {
		return nil
	}
	if !parallel {
		return ready[:1]
	}
	selected := []int{}
	for _, candidate := range ready {
		if len(selected) >= max(1, limit) {
			break
		}
		conflict := false
		for _, prior := range selected {
			if scopesOverlap(units[prior].AllowedPaths, units[candidate].AllowedPaths) {
				conflict = true
				break
			}
		}
		if !conflict {
			selected = append(selected, candidate)
		}
	}
	return selected
}

func scopesOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		for _, y := range b {
			px, py := scopePrefix(x), scopePrefix(y)
			if scopePrefixesOverlap(px, py) {
				return true
			}
		}
	}
	return false
}

func scopePrefixesOverlap(a, b string) bool {
	return missingScopePrefix(a, b) || sameOrNestedScope(a, b)
}

func missingScopePrefix(a, b string) bool {
	return a == "" || b == ""
}

func sameOrNestedScope(a, b string) bool {
	return a == b || nestedScope(a, b)
}

func nestedScope(a, b string) bool {
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func scopePrefix(pattern string) string {
	if n := strings.IndexAny(pattern, "*?["); n >= 0 {
		pattern = pattern[:n]
		if !strings.HasSuffix(pattern, "/") {
			if slash := strings.LastIndex(pattern, "/"); slash >= 0 {
				pattern = pattern[:slash+1]
			} else {
				return ""
			}
		}
	}
	return strings.TrimSuffix(pattern, "/")
}

func planForUnit(plan model.Plan, n int) model.Plan {
	plan.Units = []model.Unit{plan.Units[n]}
	return plan
}
