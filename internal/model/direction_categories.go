package model

// DirectionCategories returns the canonical ideation order. Each caller receives
// its own slice so presentation or fixture changes cannot mutate domain policy.
func DirectionCategories() []string {
	return []string{"expand", "optimize", "harden", "simplify"}
}

func IsDirectionCategory(category string) bool {
	for _, expected := range DirectionCategories() {
		if category == expected {
			return true
		}
	}
	return false
}
