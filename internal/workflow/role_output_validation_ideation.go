package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"whirlwind/internal/provider"
)

func validateIdeationOutput(purpose, text string) error {
	switch purpose {
	case "generation":
		return validateGenerationOutput(text)
	case "integration_context":
		return validateIntegrationContextOutput(text)
	default:
		return nil
	}
}

func validateGenerationOutput(text string) error {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || trimmed[0] != '[' {
		return errors.New("ideation generation must return a JSON direction array")
	}
	if _, err := decodeGeneratedIdeas(text); err != nil {
		return fmt.Errorf("ideation generation returned invalid JSON: %w", err)
	}
	return nil
}

func validateIntegrationContextOutput(text string) error {
	var fields map[string]json.RawMessage
	if err := provider.JSON(text, &fields); err != nil {
		return fmt.Errorf("integration context returned invalid JSON: %w", err)
	}
	for _, name := range []string{"scanned_integrations", "sources"} {
		var values []json.RawMessage
		if len(fields[name]) == 0 || provider.JSON(string(fields[name]), &values) != nil || values == nil {
			return fmt.Errorf("integration context is missing its %s array", name)
		}
	}
	return nil
}
