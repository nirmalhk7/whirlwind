package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func validateIdeationOutput(purpose, text string) error {
	switch {
	case purpose == "generation":
		return validateGenerationOutput(text)
	case strings.HasPrefix(purpose, "novelty:"):
		return validateNoveltyOutput(text)
	case purpose == "integration_context":
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
	var directions []model.Direction
	if err := provider.JSON(text, &directions); err != nil {
		return fmt.Errorf("ideation generation returned invalid JSON: %w", err)
	}
	return nil
}

func validateNoveltyOutput(text string) error {
	var verdict struct {
		Unique *bool `json:"unique"`
	}
	if err := provider.JSON(text, &verdict); err != nil {
		return fmt.Errorf("ideation novelty review returned invalid JSON: %w", err)
	}
	if verdict.Unique == nil {
		return errors.New("ideation novelty review is missing its unique verdict")
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
