package model

const DefaultProviderStatusCheckSeconds = 30

type ProviderStatus struct {
	QuotaPool     string         `json:"quota_pool,omitempty"`
	QuotaSnapshot *QuotaSnapshot `json:"quota_snapshot,omitempty"`
	QuotaFresh    *bool          `json:"quota_fresh"`
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Kind          string         `json:"kind"`
	Status        string         `json:"status"`
	Detail        string         `json:"detail"`
	CheckedAt     string         `json:"checked_at,omitempty"`
}
type ProviderStatuses struct {
	Providers       []ProviderStatus `json:"providers"`
	IntervalSeconds int              `json:"interval_seconds"`
	Stopped         bool             `json:"stopped"`
}
