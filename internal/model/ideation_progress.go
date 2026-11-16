package model

type IdeationDirectionProgress struct {
	Direction      string `json:"direction"`
	CandidateCount int    `json:"candidate_count"`
	Status         string `json:"status"`
	IdeaID         string `json:"idea_id,omitempty"`
	ErrorReason    string `json:"error_reason,omitempty"`
}
