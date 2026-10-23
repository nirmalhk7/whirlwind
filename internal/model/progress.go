package model

// ProgressEvidence records the best measured outcome reached within the current plan.
type ProgressEvidence struct {
	ChecksKnown       bool `json:"checks_known"`
	ReviewKnown       bool `json:"review_known"`
	PlanVersion       int  `json:"plan_version"`
	FailingChecks     int  `json:"failing_checks"`
	BlockingFindings  int  `json:"blocking_findings"`
	CompletedCriteria int  `json:"completed_criteria"`
}
