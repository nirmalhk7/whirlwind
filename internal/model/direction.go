package model

type Direction struct {
	IdeaFields         *IdeaDraft    `json:"idea_fields,omitempty"`
	ExpectedBenefit    string        `json:"expected_benefit,omitempty"`
	Problem            string        `json:"problem,omitempty"`
	Mechanism          string        `json:"mechanism,omitempty"`
	RepositoryRevision string        `json:"repository_revision,omitempty"`
	RejectionReason    string        `json:"rejection_reason,omitempty"`
	DeferralReason     string        `json:"deferral_reason,omitempty"`
	RevisitReason      string        `json:"revisit_reason,omitempty"`
	PredecessorID      string        `json:"predecessor_id,omitempty"`
	Components         []string      `json:"components,omitempty"`
	RelatedProposalIDs []string      `json:"related_proposal_ids,omitempty"`
	ApprovedPlanIDs    []string      `json:"approved_plan_ids,omitempty"`
	Uncertainty        []string      `json:"uncertainty,omitempty"`
	EffortEstimate     string        `json:"effort_estimate,omitempty"`
	ProblemKind        string        `json:"problem_kind,omitempty"`
	Rank               int           `json:"rank,omitempty"`
	Status             string        `json:"status,omitempty"`
	TaskID             string        `json:"plan_id,omitempty"`
	SourceIssue        *Issue        `json:"source_issue,omitempty"`
	Fingerprint        string        `json:"fingerprint,omitempty"`
	Scope              []string      `json:"scope,omitempty"`
	Evidence           []EvidenceRef `json:"evidence,omitempty"`
	GroupID            string        `json:"group_id,omitempty"`
	Category           string        `json:"category,omitempty"`
	ID                 string        `json:"id"`
	ProjectID          string        `json:"project_id"`
	Title              string        `json:"title"`
	Description        string        `json:"description"`
	Day                string        `json:"day"`
	Selected           bool          `json:"selected"`
}

type Handoff struct {
	AutofixProposals  []AutofixHandoff `json:"autofix_proposals,omitempty"`
	Version           int              `json:"version"`
	Worktree          string           `json:"worktree"`
	Branch            string           `json:"branch"`
	Commits           []string         `json:"commits"`
	Checkpoints       []string         `json:"checkpoints"`
	CompletedWork     []string         `json:"completed_work"`
	CompletedCriteria []string         `json:"completed_criteria"`
	FailingTests      []string         `json:"failing_tests"`
	NextAction        string           `json:"next_action"`
}
