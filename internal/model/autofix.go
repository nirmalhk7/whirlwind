package model

type AutofixHandoff struct {
	ArtifactID      string `json:"artifact_id"`
	VerificationID  string `json:"verification_id"`
	CheckID         string `json:"check_id"`
	CandidateCommit string `json:"candidate_commit"`
	CandidateTree   string `json:"candidate_tree"`
	SHA256          string `json:"sha256"`
	PlanVersion     int    `json:"plan_version"`
	Path            string `json:"path,omitempty"`
}
