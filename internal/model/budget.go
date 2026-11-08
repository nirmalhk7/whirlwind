package model

import (
	"errors"
	"math"
)

// BudgetPolicy has no inferred allowances. Finite attempts and runtime are required at admission.
type BudgetPolicy struct {
	VerificationAttempts       int     `json:"verification_attempts,omitempty" yaml:"verification_attempts"`
	VerificationRuntimeSeconds int     `json:"verification_runtime_seconds,omitempty" yaml:"verification_runtime_seconds"`
	MaxAttempts                int     `json:"max_attempts" yaml:"max_attempts"`
	MaxRuntimeSeconds          int     `json:"max_runtime_seconds" yaml:"max_runtime_seconds"`
	MaxTokens                  int64   `json:"max_tokens,omitempty" yaml:"max_tokens"`
	MaxCost                    float64 `json:"max_cost,omitempty" yaml:"max_cost"`
	AttemptTokens              int64   `json:"attempt_tokens,omitempty" yaml:"attempt_tokens"`
	AttemptCost                float64 `json:"attempt_cost,omitempty" yaml:"attempt_cost"`
}

func (b BudgetPolicy) Validate() error {
	if err := validateBudgetCeilings(b); err != nil {
		return err
	}
	if err := validateBudgetReserves(b); err != nil {
		return err
	}
	return validateBudgetAllowances(b)
}

func validateBudgetCeilings(b BudgetPolicy) error {
	if b.MaxAttempts <= 0 || b.MaxRuntimeSeconds <= 0 {
		return errors.New("configure finite max_attempts and max_runtime_seconds before unattended execution")
	}
	return nil
}

func validateBudgetReserves(b BudgetPolicy) error {
	if negativeVerificationReserve(b) || reserveExceedsCeiling(b) {
		return errors.New("verification reserves must be nonnegative and below task ceilings")
	}
	return nil
}

func negativeVerificationReserve(b BudgetPolicy) bool {
	return b.VerificationAttempts < 0 || b.VerificationRuntimeSeconds < 0
}

func reserveExceedsCeiling(b BudgetPolicy) bool {
	return b.VerificationAttempts >= b.MaxAttempts || b.VerificationRuntimeSeconds >= b.MaxRuntimeSeconds
}

func validateBudgetAllowances(b BudgetPolicy) error {
	if err := validateFiniteAllowances(b); err != nil {
		return err
	}
	if b.MaxTokens > 0 && b.AttemptTokens <= 0 {
		return errors.New("token ceiling requires an explicit attempt_tokens reservation")
	}
	if b.MaxCost > 0 && b.AttemptCost <= 0 {
		return errors.New("cost ceiling requires an explicit attempt_cost reservation")
	}
	return nil
}

func validateFiniteAllowances(b BudgetPolicy) error {
	if b.MaxTokens < 0 || b.AttemptTokens < 0 {
		return errors.New("budget allowances must be finite and nonnegative")
	}
	if !finiteNonnegativeCost(b.MaxCost) || !finiteNonnegativeCost(b.AttemptCost) {
		return errors.New("budget allowances must be finite and nonnegative")
	}
	return nil
}

func finiteNonnegativeCost(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

type BudgetReservation struct {
	RuntimeUnknown  bool    `json:"runtime_unknown,omitempty"`
	Kind            string  `json:"kind,omitempty"`
	AttemptID       string  `json:"attempt_id"`
	TaskID          string  `json:"task_id"`
	Owner           string  `json:"owner"`
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	Pool            string  `json:"pool"`
	Role            string  `json:"role"`
	Status          string  `json:"status"`
	Tokens          int64   `json:"tokens"`
	Cost            float64 `json:"cost"`
	RuntimeSeconds  int     `json:"runtime_seconds"`
	PoolConcurrency int     `json:"pool_concurrency,omitempty"`
	CreatedAt       string  `json:"created_at"`
	SettledAt       string  `json:"settled_at,omitempty"`
}
