package model

// UsageEntry keeps reported scope and independently computed attempt deltas.
type UsageEntry struct {
	Kind             string `json:"kind,omitempty"`
	Transport        string `json:"transport,omitempty"`
	ExecutionProfile string `json:"execution_profile,omitempty"`
	PlanVersion      int    `json:"plan_version,omitempty"`
	AttemptID        string `json:"attempt_id"`
	TaskID           string `json:"task_id"`
	ProjectID        string `json:"project_id"`
	OperationID      string `json:"operation_id,omitempty"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	Role             string `json:"role"`
	SessionID        string `json:"session_id"`
	Resumed          bool   `json:"resumed"`
	Usage            *Usage `json:"reported_usage"`
	Delta            *Usage `json:"attempt_usage"`
	ElapsedMS        int64  `json:"elapsed_ms"`
	RecordedAt       string `json:"recorded_at"`
}
type UsageReport struct {
	UnknownRuntimeAttempts int                 `json:"unknown_runtime_attempts"`
	PreflightFailures      int                 `json:"preflight_failures"`
	VerificationRuns       int                 `json:"verification_runs"`
	ActiveVerificationRuns int                 `json:"active_verification_runs"`
	UncertainReservations  []BudgetReservation `json:"uncertain_reservations"`
	CacheWriteTokens       int64               `json:"cache_write_tokens"`
	TaskID                 string              `json:"task_id"`
	Attempts               int                 `json:"attempts"`
	TotalTokens            int64               `json:"total_tokens"`
	InputTokens            int64               `json:"input_tokens"`
	OutputTokens           int64               `json:"output_tokens"`
	CachedTokens           int64               `json:"cached_tokens"`
	Cost                   float64             `json:"cost"`
	ElapsedMS              int64               `json:"elapsed_ms"`
	UnknownTokenAttempts   int                 `json:"unknown_token_attempts"`
	UnknownCostAttempts    int                 `json:"unknown_cost_attempts"`
	ActiveAttempts         int                 `json:"active_attempts"`
	UncertainAttempts      int                 `json:"uncertain_attempts"`
	ReservedTokens         int64               `json:"reserved_tokens"`
	ReservedCost           float64             `json:"reserved_cost"`
	ReservedRuntimeSeconds int                 `json:"reserved_runtime_seconds"`
	Entries                []UsageEntry        `json:"entries"`
}
