package model

// LocalFinalizationApproval identifies the reviewed candidate the operator approved.
type LocalFinalizationApproval struct {
	PlanVersion      int    `json:"plan_version"`
	ApprovedPlanHash string `json:"approved_plan_hash"`
	ReviewedCommit   string `json:"reviewed_commit"`
	ReviewID         string `json:"review_id"`
	VerificationID   string `json:"verification_id"`
	PolicyHash       string `json:"policy_hash"`
}

// ApprovedPlanHash returns the frozen document hash, if this plan has one.
func (plan ExecutionPlan) ApprovedPlanHash() string {
	if plan.PlanDocument == nil {
		return ""
	}
	return plan.PlanDocument.ApprovedHash
}
