package model

import (
	"encoding/json"
	"fmt"
	"math"
)

// QuotaWindow retains native subscription units; token usage is never converted to percent.
type QuotaWindow struct {
	LimitID            string   `json:"limit_id"`
	Window             string   `json:"window"`
	UsedPercent        *float64 `json:"used_percent"`
	ResetsAt           *int64   `json:"resets_at"`
	WindowDurationMins *int64   `json:"window_duration_mins,omitempty"`
}
type QuotaSnapshot struct {
	RawMetadata   json.RawMessage `json:"raw_metadata"`
	Applicability string          `json:"applicability"`
	Pool          string          `json:"pool"`
	Source        string          `json:"source"`
	ObservedAt    string          `json:"observed_at"`
	Windows       []QuotaWindow   `json:"windows"`
}

// MarshalJSON keeps historical observations explicit about unverified applicability.
func (q QuotaSnapshot) MarshalJSON() ([]byte, error) {
	type snapshot QuotaSnapshot
	if q.Applicability == "" {
		q.Applicability = "unknown"
	}
	return json.Marshal(snapshot(q))
}

type QuotaReserve struct {
	MinRemainingPercent float64 `json:"min_remaining_percent"`
	MaxAgeSeconds       int     `json:"max_age_seconds"`
	MaxConcurrent       int     `json:"max_concurrent,omitempty"`
}
type AdmissionSettings struct {
	DiscoveryBudget *BudgetPolicy           `json:"discovery_budget,omitempty"`
	DefaultBudget   *BudgetPolicy           `json:"default_budget,omitempty"`
	Pools           map[string]QuotaReserve `json:"pools,omitempty"`
}
type TaskWait struct {
	AttemptID   string `json:"attempt_id,omitempty"`
	Kind        string `json:"kind"`
	Reason      string `json:"reason"`
	Provider    string `json:"provider,omitempty"`
	Pool        string `json:"pool,omitempty"`
	Role        string `json:"role,omitempty"`
	ResumeStage string `json:"resume_stage"`
	NextCheckAt string `json:"next_check_at,omitempty"`
	PlanVersion int    `json:"plan_version"`
	PolicyHash  string `json:"policy_hash"`
}

func (a AdmissionSettings) Validate() error {
	if a.DiscoveryBudget != nil {
		if err := a.DiscoveryBudget.Validate(); err != nil {
			return err
		}
	}
	if a.DefaultBudget != nil {
		if err := a.DefaultBudget.Validate(); err != nil {
			return err
		}
	}
	for pool, r := range a.Pools {
		if !validQuotaPool(pool, r) {
			return fmt.Errorf("invalid quota reserve/freshness for pool %s", pool)
		}
	}
	return nil
}

func validQuotaPool(pool string, reserve QuotaReserve) bool {
	return validQuotaIdentity(pool, reserve) && validQuotaWindow(reserve) && validQuotaConcurrency(reserve)
}

func validQuotaIdentity(pool string, reserve QuotaReserve) bool {
	return pool != "" && reserve.MaxAgeSeconds > 0 && reserve.MaxAgeSeconds <= 86400
}

func validQuotaWindow(reserve QuotaReserve) bool {
	return reserve.MinRemainingPercent >= 0 && reserve.MinRemainingPercent < 100 && finiteQuotaPercent(reserve.MinRemainingPercent)
}

func finiteQuotaPercent(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validQuotaConcurrency(reserve QuotaReserve) bool {
	return reserve.MaxConcurrent >= 0 && reserve.MaxConcurrent <= 32
}
