package model

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"unicode"
)

// Proposal is the complete durable ledger record. Compact prompts contain only
// a bounded projection; the SQLite row retains every field for later review.
type Proposal struct {
	ID                 string        `json:"id"`
	ProjectID          string        `json:"project_id"`
	Category           string        `json:"category"`
	Problem            string        `json:"problem"`
	Mechanism          string        `json:"mechanism"`
	Components         []string      `json:"components"`
	Evidence           []EvidenceRef `json:"evidence"`
	RepositoryRevision string        `json:"repository_revision"`
	Status             string        `json:"status"`
	RejectionReason    string        `json:"rejection_reason,omitempty"`
	DeferralReason     string        `json:"deferral_reason,omitempty"`
	Title              string        `json:"title"`
	Description        string        `json:"description"`
	Fingerprint        string        `json:"fingerprint,omitempty"`
	PredecessorID      string        `json:"predecessor_id,omitempty"`
	RevisitReason      string        `json:"revisit_reason,omitempty"`
	RelatedProposalIDs []string      `json:"related_proposal_ids,omitempty"`
	ApprovedPlanIDs    []string      `json:"approved_plan_ids,omitempty"`
	Legacy             bool          `json:"legacy,omitempty"`
	CreatedAt          string        `json:"created_at,omitempty"`
}

// ProposalReconsideration is a supervisor-owned authorization from a human to
// revisit one suppressed proposal fingerprint exactly once.
type ProposalReconsideration struct {
	ID                        string `json:"id"`
	ProjectID                 string `json:"project_id"`
	PredecessorID             string `json:"predecessor_id"`
	Reason                    string `json:"reason"`
	Status                    string `json:"status"`
	CandidateID               string `json:"candidate_id,omitempty"`
	RequestedDirectionVersion int    `json:"requested_direction_version"`
	CreatedAt                 string `json:"created_at,omitempty"`
}

type ProposalQuery struct {
	ProjectID  string
	Terms      string
	Category   string
	Components []string
	Statuses   []string
	Limit      int
}

// ProposalFingerprint excludes title and hashes normalized project/problem/
// mechanism/components so renaming or category changes cannot make a proposal new.
func ProposalFingerprint(project, problem, mechanism string, components []string) string {
	problem, mechanism = proposalTerms(problem), proposalTerms(mechanism)
	parts := make([]string, 0, len(components))
	for _, component := range components {
		if value := proposalTerms(component); value != "" {
			parts = append(parts, value)
		}
	}
	parts = slices.Compact(slices.Sorted(slices.Values(parts)))
	if strings.TrimSpace(project) == "" || problem == "" || mechanism == "" || len(parts) == 0 {
		return ""
	}
	canonical := strings.Join([]string{strings.TrimSpace(project), problem, mechanism, strings.Join(parts, "\x1f")}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func proposalTerms(value string) string {
	var builder strings.Builder
	space := false
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			builder.WriteRune(r)
			space = false
		} else if builder.Len() > 0 && !space {
			builder.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(builder.String())
}
