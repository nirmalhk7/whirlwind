package model

import (
	"encoding/json"
	"fmt"
)

const PersonalMode = "personal"
const AgenticMode = "agentic"

func EffectiveMode(mode string) string {
	if mode == "" {
		return PersonalMode
	}
	return mode
}
func ValidateMode(mode string) error {
	switch EffectiveMode(mode) {
	case PersonalMode, AgenticMode:
		return nil
	}
	return fmt.Errorf("mode must be personal or agentic")
}

type IntegrationSource struct {
	Provider string   `json:"provider"`
	Ref      string   `json:"ref"`
	Scopes   []string `json:"scopes,omitempty"`
}
type IntegrationTarget struct {
	Provider string `json:"provider"`
	Ref      string `json:"ref"`
}
type IntegrationSettings struct {
	Discovery   string                       `json:"discovery,omitempty"`
	Sources     []IntegrationSource          `json:"sources,omitempty"`
	Publication map[string]IntegrationTarget `json:"publication,omitempty"`
}
type WorkflowConfig struct {
	ActiveMode      string              `json:"active_mode"`
	ConfiguredMode  string              `json:"configured_mode"`
	RestartRequired bool                `json:"restart_required"`
	Integrations    IntegrationSettings `json:"integrations"`
}
type SourceRef struct {
	Provider   string `json:"provider,omitempty"`
	Host       string `json:"host"`
	Connection string `json:"connection"`
	Repository string `json:"repository"`
	ID         string `json:"id"`
	URL        string `json:"url,omitempty"`
	Revision   string `json:"revision,omitempty"`
}

func (t Task) Cancelled() bool { return t.Stage == "cancelled" }

type PlanDocument struct {
	Path         string `json:"path"`
	Hash         string `json:"hash"`
	CreatedAt    string `json:"created_at,omitempty"`
	RepoName     string `json:"repo_name,omitempty"`
	PlanVersion  int    `json:"plan_version,omitempty"`
	ApprovedHash string `json:"approved_hash,omitempty"`
	PolicyHash   string `json:"policy_hash,omitempty"`
	ApprovedAt   string `json:"approved_at,omitempty"`
}

type PlanDraft struct {
	RepoName    string `json:"repo_name"`
	CreatedAt   string `json:"created_at"`
	Path        string `json:"path"`
	ProjectID   string `json:"project_id"`
	PlanVersion int    `json:"plan_version"`
	TaskID      string `json:"task_id"`
}
type Submission struct {
	Stage            string          `json:"stage"`
	OriginalWorktree string          `json:"original_worktree"`
	OriginalBranch   string          `json:"original_branch"`
	ReviewedCommit   string          `json:"reviewed_commit"`
	Title            string          `json:"title"`
	Body             string          `json:"body"`
	Groups           json.RawMessage `json:"groups"`
}

// LocalDelivery records a local-only promotion of the exact accepted candidate
// commit. Its provenance stays tied to the reviewed commit and verification.
type LocalDelivery struct {
	UnmetRequirements []string   `json:"unmet_requirements,omitempty"`
	Adherence         *Adherence `json:"adherence,omitempty"`
	VerificationID    string     `json:"verification_id,omitempty"`
	OriginalWorktree  string     `json:"original_worktree,omitempty"`
	OriginalBranch    string     `json:"original_branch,omitempty"`
	Worktree          string     `json:"worktree,omitempty"`
	Branch            string     `json:"branch,omitempty"`
	ReviewedCommit    string     `json:"reviewed_commit,omitempty"`
	Base              string     `json:"base,omitempty"`
	Tree              string     `json:"tree,omitempty"`
	ReviewID          string     `json:"review_id,omitempty"`
	PolicyHash        string     `json:"policy_hash,omitempty"`
	Message           string     `json:"message,omitempty"`
	Diff              string     `json:"diff,omitempty"`
	FinishedAt        string     `json:"finished_at,omitempty"`
	PlanVersion       int        `json:"plan_version,omitempty"`
	Score             float64    `json:"score,omitempty"`
	Stage             string     `json:"stage,omitempty"`
}

type ReviewerWorktree struct {
	ID             string `json:"id"`
	TaskID         string `json:"task_id"`
	SourceWorktree string `json:"source_worktree"`
	Worktree       string `json:"worktree"`
	Commit         string `json:"commit"`
	Perspective    string `json:"perspective"`
	PlanVersion    int    `json:"plan_version"`
}
