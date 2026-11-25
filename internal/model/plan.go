package model

import (
	"slices"
)

type Issue struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	UpdatedAt string   `json:"updated_at"`
	State     string   `json:"state"`
	Labels    []string `json:"labels"`
	URL       string   `json:"url"`
}

// Eligible requires an open issue carrying the whirlwind:ready label before automatic execution.
func (i Issue) Eligible() bool {
	if i.State != "OPEN" && i.State != "open" {
		return false
	}
	return slices.Contains(i.Labels, "whirlwind:ready")
}

type ExecutionPlan struct {
	IdeaID                 string                  `json:"idea_id,omitempty"`
	IdeationRunID          string                  `json:"ideation_run_id,omitempty"`
	Mode                   string                  `json:"mode,omitempty"`
	Sources                []SourceRef             `json:"sources,omitempty"`
	PlanDocument           *PlanDocument           `json:"plan_document,omitempty"`
	Submission             *Submission             `json:"submission,omitempty"`
	Delivery               *LocalDelivery          `json:"local_delivery,omitempty"`
	Progress               *ProgressEvidence       `json:"progress,omitempty"`
	Selection              *SelectionAuthorization `json:"selection,omitempty"`
	Budget                 *BudgetPolicy           `json:"budget,omitempty"`
	Wait                   *ExecutionPlanWait      `json:"wait,omitempty"`
	ImplementationAttempts []string                `json:"implementation_attempts,omitempty"`
	LastAttemptID          string                  `json:"last_attempt_id,omitempty"`
	VerificationID         string                  `json:"verification_id,omitempty"`
	ReviewID               string                  `json:"review_id,omitempty"`
	Approval               *Provenance             `json:"approval_evidence,omitempty"`
	ResumeStage            string                  `json:"resume_stage,omitempty"`
	EligibilityOverride    bool                    `json:"eligibility_override,omitempty"`
	ID                     string                  `json:"id"`
	ProjectID              string                  `json:"project_id"`
	Issue                  Issue                   `json:"issue"`
	PlanVersion            int                     `json:"plan_version"`
	Stage                  string                  `json:"stage"`
	Plan                   *PlanSpecification      `json:"plan,omitempty"`
	Worktree               string                  `json:"worktree,omitempty"`
	Branch                 string                  `json:"branch,omitempty"`
	Base                   string                  `json:"base,omitempty"`
	ReviewedCommit         string                  `json:"reviewed_commit,omitempty"`
	Score                  float64                 `json:"score"`
	ReviewPasses           int                     `json:"review_passes"`
	UnchangedFailures      int                     `json:"unchanged_failures"`
	Recoveries             int                     `json:"recoveries"`
	RecoveryGeneration     int                     `json:"recovery_generation,omitempty"`
	Fingerprint            string                  `json:"fingerprint,omitempty"`
	Risk                   string                  `json:"risk,omitempty"`
	Approved               bool                    `json:"approved"`
	ChecksPassed           bool                    `json:"checks_passed"`
	PR                     string                  `json:"pr,omitempty"`
	Error                  string                  `json:"error,omitempty"`
	Handoff                Handoff                 `json:"handoff"`
}

type PlanSpecification struct {
	Approval                 *PlanApproval `json:"approval,omitempty"`
	Requirements             []Requirement `json:"requirements,omitempty"`
	Checks                   []Check       `json:"checks,omitempty"`
	RepositoryTestCommands   [][]string    `json:"repository_test_commands,omitempty"`
	RepositoryBuildCommands  [][]string    `json:"repository_build_commands,omitempty"`
	RepositoryTestInventory  string        `json:"repository_test_inventory,omitempty"`
	RepositoryBuildInventory string        `json:"repository_build_inventory,omitempty"`
	Scope                    []string      `json:"scope,omitempty"`
	Exclusions               []string      `json:"exclusions,omitempty"`
	Steps                    []string      `json:"steps,omitempty"`
	What                     string        `json:"what,omitempty"`
	Why                      string        `json:"why,omitempty"`
	Benefit                  string        `json:"benefit,omitempty"`
	Where                    []string      `json:"where,omitempty"`
	Specification            string        `json:"specification,omitempty"`
	Objective                string        `json:"objective"`
	AcceptanceCriteria       []string      `json:"acceptance_criteria"`
	Questions                []Question    `json:"questions"`
	Units                    []Unit        `json:"units"`
}

type Unit struct {
	AttemptID    string   `json:"attempt_id,omitempty"`
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
