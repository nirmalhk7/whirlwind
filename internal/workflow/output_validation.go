package workflow

import (
	"fmt"
	"whirlwind/internal/contracts"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type outputValidationError struct{ cause error }

func (e *outputValidationError) Error() string { return "invalid role output: " + e.cause.Error() }
func (e *outputValidationError) Unwrap() error { return e.cause }

func validateAgentOutput(input provider.Input, raw string) error {
	if input.OutputContract == "" {
		return nil
	}
	result, _, _ := decodeLearning(raw)
	var value any
	err := provider.JSON(result, &value)
	if err == nil {
		err = contracts.Validate(input.OutputContract, value)
	}
	if err == nil && input.ValidateResult != nil {
		err = input.ValidateResult(result)
	}
	if err != nil {
		return &outputValidationError{cause: err}
	}
	return nil
}

func (a *agentRun) repairOutput(sourceErr error) {
	a.input.ReadOnly = true
	a.input.Prompt += "\nRepair only the response format/content; do not execute tools, redo implementation, or publish. Return the complete corrected result.\nValidation errors:\n" + string(store.Prefix([]byte(fmt.Sprint(sourceErr)), 4096))
}
