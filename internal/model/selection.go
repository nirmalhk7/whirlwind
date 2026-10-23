package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// SelectionAuthorization records the human-selected objective before any planning.
type SelectionAuthorization struct {
	CandidateID       string   `json:"candidate_id"`
	CandidateVersion  int      `json:"candidate_version"`
	Objective         string   `json:"objective"`
	Scope             []string `json:"scope"`
	PolicyHash        string   `json:"policy_hash"`
	SourceFingerprint string   `json:"source_fingerprint,omitempty"`
	SelectedAt        string   `json:"selected_at"`
}

type EvidenceRef struct {
	Provider    string `json:"provider,omitempty"`
	Connection  string `json:"connection,omitempty"`
	URL         string `json:"url,omitempty"`
	RetrievedAt string `json:"retrieved_at,omitempty"`
	AttemptID   string `json:"attempt_id,omitempty"`
	Kind        string `json:"kind"`
	Ref         string `json:"ref"`
	Revision    string `json:"revision,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

type IdeationBrief struct {
	Goal          string `json:"goal"`
	Constraints   string `json:"constraints"`
	Priorities    string `json:"priorities"`
	MaxCandidates int    `json:"max_candidates,omitempty"`
}

func (t Task) HasSelection() bool {
	return t.Selection != nil && t.Selection.CandidateID != "" && t.Selection.SelectedAt != ""
}

// IssueFingerprint intentionally excludes timestamps and labels: metadata cannot expand scope.
func IssueFingerprint(issue Issue) string {
	data, _ := json.Marshal(struct {
		Number      int
		Title, Body string
	}{issue.Number, issue.Title, issue.Body})
	return Fingerprint(data)
}

func Fingerprint(data []byte) string {
	value := sha256.Sum256(data)
	return hex.EncodeToString(value[:])
}
