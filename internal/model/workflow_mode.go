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
	ApprovedHash string `json:"approved_hash,omitempty"`
	PolicyHash   string `json:"policy_hash,omitempty"`
	ApprovedAt   string `json:"approved_at,omitempty"`
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
