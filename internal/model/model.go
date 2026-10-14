package model

import (
	"encoding/json"
	"slices"
)

type Check struct {
	Name           string   `json:"name" yaml:"name"`
	Command        []string `json:"command" yaml:"command"`
	TimeoutSeconds int      `json:"timeout_seconds" yaml:"timeout_seconds"`
}
type RiskRule struct {
	APISymbols []string `json:"api_symbols,omitempty" yaml:"api_symbols"`
	CodeTypes  []string `json:"code_types,omitempty" yaml:"code_types"`
	Level      string   `json:"level" yaml:"level"`
	Paths      []string `json:"paths" yaml:"paths"`
	Checks     []Check  `json:"checks" yaml:"checks"`
}
type Embeddings struct {
	Endpoint string `json:"endpoint" yaml:"endpoint"`
	Model    string `json:"model" yaml:"model"`
	KeyEnv   string `json:"key_env" yaml:"key_env"`
	Enabled  bool   `json:"enabled" yaml:"enabled"`
}
type Policy struct {
	RunTimeoutSeconds int         `json:"run_timeout_seconds,omitempty" yaml:"run_timeout_seconds"`
	Checks            []Check     `json:"checks" yaml:"checks"`
	RiskRules         []RiskRule  `json:"risk_rules" yaml:"risk_rules"`
	AllowedPaths      []string    `json:"allowed_paths" yaml:"allowed_paths"`
	Commands          [][]string  `json:"commands" yaml:"commands"`
	Providers         []string    `json:"providers" yaml:"providers"`
	GlobalSlots       int         `json:"global_slots" yaml:"global_slots"`
	ProjectSlots      int         `json:"project_slots" yaml:"project_slots"`
	Embeddings        *Embeddings `json:"embeddings,omitempty" yaml:"embeddings"`
}
type Project struct {
	DefaultBranch string `json:"default_branch"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Repo          string `json:"repo"`
	Path          string `json:"path"`
	Policy        Policy `json:"policy"`
	PolicyHash    string `json:"policy_hash"`
	WebhookURL    string `json:"webhook_url,omitempty"`
}
type Provider struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Command      []string `json:"command"`
	Capabilities []string `json:"capabilities"`
	Available    bool     `json:"available"`
	Quota        string   `json:"quota"`
	ResetAt      string   `json:"reset_at,omitempty"`
	Cost         *float64 `json:"cost,omitempty"`
	Limitation   string   `json:"limitation,omitempty"`
}
type Issue struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	UpdatedAt string   `json:"updated_at"`
	State     string   `json:"state"`
	Labels    []string `json:"labels"`
	URL       string   `json:"url"`
}

// Eligible requires an open issue carrying the whirlwind:plan label before automatic execution.
func (i Issue) Eligible() bool {
	if i.State != "OPEN" && i.State != "open" {
		return false
	}
	return slices.Contains(i.Labels, "whirlwind:plan")
}

type Task struct {
	ResumeStage         string  `json:"resume_stage,omitempty"`
	EligibilityOverride bool    `json:"eligibility_override,omitempty"`
	ID                  string  `json:"id"`
	ProjectID           string  `json:"project_id"`
	Issue               Issue   `json:"issue"`
	PlanVersion         int     `json:"plan_version"`
	Stage               string  `json:"stage"`
	Plan                *Plan   `json:"plan,omitempty"`
	Worktree            string  `json:"worktree,omitempty"`
	Branch              string  `json:"branch,omitempty"`
	Base                string  `json:"base,omitempty"`
	ReviewedCommit      string  `json:"reviewed_commit,omitempty"`
	Score               float64 `json:"score"`
	ReviewPasses        int     `json:"review_passes"`
	UnchangedFailures   int     `json:"unchanged_failures"`
	Recoveries          int     `json:"recoveries"`
	Fingerprint         string  `json:"fingerprint,omitempty"`
	Risk                string  `json:"risk,omitempty"`
	Approved            bool    `json:"approved"`
	ChecksPassed        bool    `json:"checks_passed"`
	PR                  string  `json:"pr,omitempty"`
	Error               string  `json:"error,omitempty"`
	Handoff             Handoff `json:"handoff"`
}
type Plan struct {
	Objective          string     `json:"objective"`
	AcceptanceCriteria []string   `json:"acceptance_criteria"`
	Questions          []Question `json:"questions"`
	Units              []Unit     `json:"units"`
}
type Question struct {
	Options []string `json:"options,omitempty"`
	Secret  bool     `json:"secret,omitempty"`
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Answer  string   `json:"answer"`
}
type Unit struct {
	Base         string   `json:"base,omitempty"`
	ID           string   `json:"id"`
	Objective    string   `json:"objective"`
	Dependencies []string `json:"dependencies"`
	AllowedPaths []string `json:"allowed_paths"`
	Stage        string   `json:"stage"`
	Worktree     string   `json:"worktree,omitempty"`
	Branch       string   `json:"branch,omitempty"`
	Commit       string   `json:"commit,omitempty"`
}
type Finding struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
	Resolved bool   `json:"resolved"`
}
type Review struct {
	Score    float64   `json:"score"`
	Findings []Finding `json:"findings"`
	Summary  string    `json:"summary"`
}
type Direction struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Day         string `json:"day"`
	Selected    bool   `json:"selected"`
}
type Handoff struct {
	Version           int      `json:"version"`
	Worktree          string   `json:"worktree"`
	Branch            string   `json:"branch"`
	Commits           []string `json:"commits"`
	Checkpoints       []string `json:"checkpoints"`
	CompletedWork     []string `json:"completed_work"`
	CompletedCriteria []string `json:"completed_criteria"`
	FailingTests      []string `json:"failing_tests"`
	NextAction        string   `json:"next_action"`
}
type Event struct {
	ID         int64           `json:"id"`
	Kind       string          `json:"kind"`
	ResourceID string          `json:"resource_id"`
	Data       json.RawMessage `json:"data"`
	CreatedAt  string          `json:"created_at"`
}
type Record struct {
	ID        string          `json:"id"`
	Version   int             `json:"version"`
	Data      json.RawMessage `json:"data"`
	UpdatedAt string          `json:"updated_at"`
}
type Notification struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id,omitempty"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	Resolved bool   `json:"resolved"`
}

type QuestionSet struct {
	ID          string            `json:"id"`
	TaskID      string            `json:"task_id"`
	Provider    string            `json:"provider"`
	Questions   []Question        `json:"questions"`
	Answers     map[string]string `json:"answers"`
	Status      string            `json:"status"`
	PlanVersion int               `json:"plan_version"`
}
