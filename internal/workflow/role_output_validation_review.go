package workflow

import (
	"errors"
	"fmt"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func validateReviewOutput(text string) error {
	var review model.Review
	if err := provider.JSON(text, &review); err != nil {
		return fmt.Errorf("review output must be valid Review JSON: %w", err)
	}
	if strings.TrimSpace(review.Summary) == "" {
		return errors.New("review output is missing its summary")
	}
	if err := validateReviewFindings(review.Findings); err != nil {
		return fmt.Errorf("review output has invalid findings: %w", err)
	}
	return validateRequirementJudgments(review.Requirements)
}

func validateRequirementJudgments(requirements []model.RequirementResult) error {
	for _, result := range requirements {
		if strings.TrimSpace(result.ID) == "" || strings.TrimSpace(result.Explanation) == "" {
			return errors.New("review requirement judgment needs an ID and explanation")
		}
		switch result.Status {
		case "pass", "fail", "unverified":
		default:
			return fmt.Errorf("review requirement %s has invalid status %q", result.ID, result.Status)
		}
	}
	return nil
}
