package model

type IdeaDraft struct {
	What       string `json:"what"`
	Why        string `json:"why"`
	Where      string `json:"where"`
	HowItHelps string `json:"how_it_helps"`
}

// Idea is the persisted proposal and its independent human decision. Archival
// and novelty remain separate from the five-state decision lifecycle.
type Idea struct {
	Title              string        `json:"idea_title"`
	ReconsiderationID  string        `json:"reconsideration_id,omitempty"`
	ID                 string        `json:"idea_id"`
	ProjectID          string        `json:"project_id"`
	ProjectName        string        `json:"project_name"`
	RunID              string        `json:"run_id"`
	Direction          string        `json:"direction"`
	What               string        `json:"what"`
	Why                string        `json:"why"`
	Where              string        `json:"where"`
	HowItHelps         string        `json:"how_it_helps"`
	Mechanism          string        `json:"mechanism,omitempty"`
	Components         []string      `json:"components,omitempty"`
	Evidence           []EvidenceRef `json:"evidence,omitempty"`
	RepositoryRevision string        `json:"repository_revision,omitempty"`
	Fingerprint        string        `json:"fingerprint"`
	NoveltyOutcome     string        `json:"novelty_outcome"`
	NoveltyMethod      string        `json:"novelty_method"`
	RelatedID          string        `json:"related_id,omitempty"`
	PredecessorID      string        `json:"predecessor_id,omitempty"`
	RevisitReason      string        `json:"revisit_reason,omitempty"`
	DecisionStatus     string        `json:"decision_status"`
	PlanID             string        `json:"plan_id,omitempty"`
	ArchivedAt         string        `json:"archived_at,omitempty"`
	ArchiveReason      string        `json:"archive_reason,omitempty"`
	ErrorReason        string        `json:"error_reason,omitempty"`
}
