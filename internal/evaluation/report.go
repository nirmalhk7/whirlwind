// Package evaluation reports observed outcomes without equating publication with correctness.
package evaluation

import (
	"encoding/json"
	"slices"
	"sort"
	"whirlwind/internal/model"
)

type Sample struct {
	Operation     bool
	Task          model.Task
	Usage         model.UsageReport
	Attempts      []model.Attempt
	Entries       []model.UsageEntry
	Category      string
	Decision      string
	Decisions     []string
	Verified      bool
	Interventions int
}
type Group struct {
	PreflightFailures    int    `json:"preflight_failures"`
	Provider             string `json:"provider"`
	Model                string `json:"model"`
	Thinking             string `json:"thinking"`
	Role                 string `json:"role"`
	Category             string `json:"task_class"`
	Attempts             int    `json:"attempts"`
	FailedAttempts       int    `json:"failed_attempts"`
	UnfinishedAttempts   int    `json:"unfinished_attempts"`
	ReportedTokens       int64  `json:"reported_tokens"`
	UnknownTokenAttempts int    `json:"unknown_token_attempts"`
	ElapsedMS            int64  `json:"elapsed_ms"`
	VerifiedTasks        int    `json:"verified_tasks"`
}
type TaskResult struct {
	ID            string            `json:"id"`
	Stage         string            `json:"stage"`
	Verified      bool              `json:"verified"`
	PRCreated     bool              `json:"pr_created"`
	Decision      string            `json:"observed_decision,omitempty"`
	Usage         model.UsageReport `json:"usage"`
	Interventions int               `json:"interventions"`
}
type Report struct {
	UnknownRuntimeAttempts   int          `json:"unknown_runtime_attempts"`
	PreflightFailures        int          `json:"preflight_failures"`
	VerificationRuns         int          `json:"verification_runs"`
	CorpusID                 string       `json:"corpus_id,omitempty"`
	DiscoveryRuns            int          `json:"discovery_runs"`
	OperationRuns            int          `json:"operation_runs"`
	Tasks                    int          `json:"tasks"`
	Attempts                 int          `json:"attempts"`
	VerifiedTasks            int          `json:"verified_tasks"`
	CreatedPRs               int          `json:"created_prs"`
	MergedPRs                int          `json:"merged_prs"`
	ClosedPRs                int          `json:"closed_prs"`
	UserAcceptedTasks        int          `json:"user_accepted_tasks"`
	UnknownAcceptanceTasks   int          `json:"unknown_acceptance_tasks"`
	Regressions              int          `json:"observed_regressions"`
	Reverts                  int          `json:"observed_reverts"`
	UnknownPostMergeOutcomes int          `json:"unknown_post_merge_outcomes"`
	ReportedTokens           int64        `json:"reported_tokens"`
	ReportedCost             float64      `json:"reported_cost"`
	UnknownTokenAttempts     int          `json:"unknown_token_attempts"`
	UnknownCostAttempts      int          `json:"unknown_cost_attempts"`
	ElapsedMS                int64        `json:"active_elapsed_ms"`
	Interventions            int          `json:"interventions"`
	Groups                   []Group      `json:"route_comparisons"`
	Results                  []TaskResult `json:"results"`
	Recommendation           string       `json:"recommendation"`
}

func Build(samples []Sample) Report {
	r := Report{Groups: []Group{}, Results: []TaskResult{}, Recommendation: "Compare matching task classes and whole-task outcomes before changing routes. Attempt success alone does not prove quality. Explicit provider chains retain their configured order."}
	groups := map[string]*Group{}
	seen := map[string]bool{}
	for _, s := range samples {
		if s.Operation {
			r.OperationRuns++
			if slices.ContainsFunc(s.Attempts, func(a model.Attempt) bool { return a.Role == "ideate" }) {
				r.DiscoveryRuns++
			}
		} else {
			r.Tasks++
		}
		r.Attempts += s.Usage.Attempts
		r.PreflightFailures += s.Usage.PreflightFailures
		r.VerificationRuns += s.Usage.VerificationRuns
		r.UnknownRuntimeAttempts += s.Usage.UnknownRuntimeAttempts
		r.ReportedTokens += s.Usage.TotalTokens
		r.ReportedCost += s.Usage.Cost
		r.UnknownTokenAttempts += s.Usage.UnknownTokenAttempts
		r.UnknownCostAttempts += s.Usage.UnknownCostAttempts
		r.ElapsedMS += s.Usage.ElapsedMS
		r.Interventions += s.Interventions
		if s.Verified {
			r.VerifiedTasks++
		}
		if s.Task.PR != "" {
			r.CreatedPRs++
		}
		decisions := append(slices.Clone(s.Decisions), s.Decision)
		if slices.Contains(decisions, "merged") {
			r.MergedPRs++
			if !slices.Contains(decisions, "accepted") && !slices.Contains(decisions, "regression") && !slices.Contains(decisions, "reverted") {
				r.UnknownPostMergeOutcomes++
			}
		}
		if slices.Contains(decisions, "closed") {
			r.ClosedPRs++
		}
		if slices.Contains(decisions, "accepted") {
			r.UserAcceptedTasks++
		}
		if slices.Contains(decisions, "regression") {
			r.Regressions++
		}
		if slices.Contains(decisions, "reverted") {
			r.Reverts++
		}
		if !s.Operation && !slices.Contains(decisions, "accepted") && !slices.Contains(decisions, "rejected") {
			r.UnknownAcceptanceTasks++
		}
		usage := s.Usage
		usage.Entries = nil
		r.Results = append(r.Results, TaskResult{s.Task.ID, s.Task.Stage, s.Verified, s.Task.PR != "", s.Decision, usage, s.Interventions})
		entries := map[string]model.UsageEntry{}
		for _, entry := range s.Entries {
			entries[entry.AttemptID] = entry
		}
		for _, a := range s.Attempts {
			thinking := a.EffectiveThinking
			if thinking == nil {
				thinking = a.Thinking
			}
			raw, _ := json.Marshal(thinking)
			name := a.ResolvedModel
			if name == "" {
				name = a.Model
			}
			identity := []string{a.Provider, name, string(raw), a.Role, s.Category}
			keyBytes, _ := json.Marshal(identity)
			key := string(keyBytes)
			g := groups[key]
			if g == nil {
				g = &Group{Provider: a.Provider, Model: name, Thinking: string(raw), Role: a.Role, Category: s.Category}
				groups[key] = g
			}
			entry := entries[a.AttemptID]
			if entry.Kind == "preflight" {
				g.PreflightFailures++
				continue
			}
			g.Attempts++
			if a.Success != nil && !*a.Success {
				g.FailedAttempts++
			}
			if !a.Finished {
				g.UnfinishedAttempts++
			}
			g.ElapsedMS += entry.ElapsedMS
			if entry.Delta == nil || entry.Delta.TotalTokens == nil {
				g.UnknownTokenAttempts++
			} else {
				g.ReportedTokens += *entry.Delta.TotalTokens
			}
			if s.Verified && !seen[key+"\x00"+s.Task.ID] {
				g.VerifiedTasks++
				seen[key+"\x00"+s.Task.ID] = true
			}
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		r.Groups = append(r.Groups, *groups[key])
	}
	return r
}

type Comparison struct {
	Eligible           bool   `json:"eligible"`
	Reason             string `json:"reason"`
	ReportedTokenDelta int64  `json:"reported_token_delta"`
}

func Compare(baseline, candidate Report) Comparison {
	result := Comparison{ReportedTokenDelta: candidate.ReportedTokens - baseline.ReportedTokens}
	switch {
	case baseline.CorpusID == "" || baseline.CorpusID != candidate.CorpusID:
		result.Reason = "Declare the same versioned corpus identity before comparing runs."
	case baseline.Tasks == 0 || baseline.Tasks != candidate.Tasks:
		result.Reason = "Use the same nonempty evaluation corpus."
	case baseline.UnknownTokenAttempts != 0 || candidate.UnknownTokenAttempts != 0:
		result.Reason = "Incomplete telemetry prevents a consumption claim."
	case baseline.VerifiedTasks != baseline.Tasks || candidate.VerifiedTasks != candidate.Tasks:
		result.Reason = "Every corpus task must have verified acceptance coverage."
	case candidate.VerifiedTasks < baseline.VerifiedTasks || candidate.UserAcceptedTasks < baseline.UserAcceptedTasks || candidate.Regressions > baseline.Regressions || candidate.Reverts > baseline.Reverts || candidate.Interventions > baseline.Interventions:
		result.Reason = "Quality or required intervention regressed."
	case candidate.ReportedTokens >= baseline.ReportedTokens:
		result.Reason = "Reported consumption did not improve."
	default:
		result.Eligible = true
		result.Reason = "Reported consumption improved under these observations; inspect acceptance coverage and task comparability before enabling the optimization."
	}
	return result
}
