package workflow

import (
	"errors"
	"strings"
)

var roleOutputValidators = map[string]func(string, string) error{
	"plan":      validatePlanOutput,
	"review":    func(_, text string) error { return validateReviewOutput(text) },
	"finalizer": validateFinalizerOutput,
	"code":      func(_, text string) error { return validateCodeOutput(text) },
	"ideate":    validateIdeationOutput,
}

func validateRoleOutput(role, purpose, text string) error {
	if purpose == "provider_test" {
		return nil
	}
	validate, ok := roleOutputValidators[role]
	if !ok {
		return nil
	}
	return validate(purpose, text)
}

func validatePlanOutput(_, text string) error {
	_, err := decodePlan(text)
	return err
}

func validateFinalizerOutput(purpose, text string) error {
	if purpose == "local_delivery_summary" && strings.TrimSpace(text) == "" {
		return errors.New("finalizer output is missing the local delivery summary")
	}
	return nil
}

func validateCodeOutput(text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("coding output is missing its final response")
	}
	return nil
}
