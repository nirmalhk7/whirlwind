package control

import "whirlwind/internal/model"

// FinalizationReport is the local supervisor response for finalization.inspect.
type FinalizationReport struct {
	Task             model.ExecutionPlan  `json:"plan_id"`
	Review           model.Review         `json:"review"`
	Adherence        model.Adherence      `json:"adherence"`
	ApprovedPlanHash string               `json:"approved_plan_hash"`
	PolicyHash       string               `json:"policy_hash"`
	Diff             FinalizationDiffPage `json:"diff"`
	Accepted         bool                 `json:"accepted"`
}

// FinalizationDiffPage contains one page of the exact candidate diff.
type FinalizationDiffPage struct {
	Content    string `json:"content"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	HasMore    bool   `json:"has_more"`
}
