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
	if b.MaxAttempts <= 0 || b.MaxRuntimeSeconds <= 0 {
		return errors.New("configure finite max_attempts and max_runtime_seconds before unattended execution")
	}
	if b.VerificationAttempts < 0 || b.VerificationRuntimeSeconds < 0 || b.VerificationAttempts >= b.MaxAttempts || b.VerificationRuntimeSeconds >= b.MaxRuntimeSeconds {
		return errors.New("verification reserves must be nonnegative and below task ceilings")
	}
	if b.MaxTokens < 0 || b.AttemptTokens < 0 || b.MaxCost < 0 || b.AttemptCost < 0 || math.IsNaN(b.MaxCost) || math.IsInf(b.MaxCost, 0) || math.IsNaN(b.AttemptCost) || math.IsInf(b.AttemptCost, 0) {
		return errors.New("budget allowances must be finite and nonnegative")
	}
	if b.MaxTokens > 0 && b.AttemptTokens <= 0 {
		return errors.New("token ceiling requires an explicit attempt_tokens reservation")
	}
	if b.MaxCost > 0 && b.AttemptCost <= 0 {
		return errors.New("cost ceiling requires an explicit attempt_cost reservation")
	}
	return nil
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
