package model

// LocalFinalization binds a local delivery reference to accepted evidence.
// Its key is the existing plan/version identity, never a new execution ID.
type LocalFinalization struct {
	PlanID            string `json:"plan_id"`
	ProjectID         string `json:"project_id"`
	PlanVersion       int    `json:"plan_version"`
	DocumentHash      string `json:"document_hash"`
	ConfigurationHash string `json:"configuration_hash"`
	ReviewedCommit    string `json:"reviewed_commit"`
	Commit            string `json:"delivery_commit"`
	Tree              string `json:"tree"`
	Base              string `json:"base"`
	Branch            string `json:"branch"`
	Worktree          string `json:"worktree"`
	ReviewID          string `json:"review_id"`
	VerificationID    string `json:"verification_id"`
	PolicyHash        string `json:"policy_hash"`
}
