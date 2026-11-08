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
		accumulateSampleTotals(&r, s)
		accumulateDecisions(&r, s)
		appendTaskResult(&r, s)
		accumulateAttemptGroups(groups, seen, s)
	}
	r.Groups = orderedGroups(groups)
	return r
}

func accumulateSampleTotals(report *Report, sample Sample) {
	if sample.Operation {
		report.OperationRuns++
		if slices.ContainsFunc(sample.Attempts, func(a model.Attempt) bool { return a.Role == "ideate" }) {
			report.DiscoveryRuns++
		}
	} else {
		report.Tasks++
	}
	report.Attempts += sample.Usage.Attempts
	report.PreflightFailures += sample.Usage.PreflightFailures
	report.VerificationRuns += sample.Usage.VerificationRuns
	report.UnknownRuntimeAttempts += sample.Usage.UnknownRuntimeAttempts
	report.ReportedTokens += sample.Usage.TotalTokens
	report.ReportedCost += sample.Usage.Cost
	report.UnknownTokenAttempts += sample.Usage.UnknownTokenAttempts
	report.UnknownCostAttempts += sample.Usage.UnknownCostAttempts
	report.ElapsedMS += sample.Usage.ElapsedMS
	report.Interventions += sample.Interventions
	if sample.Verified {
		report.VerifiedTasks++
	}
	if sample.Task.PR != "" {
		report.CreatedPRs++
	}
}

func accumulateDecisions(report *Report, sample Sample) {
	decisions := append(slices.Clone(sample.Decisions), sample.Decision)
	if slices.Contains(decisions, "merged") {
		report.MergedPRs++
		if !slices.Contains(decisions, "accepted") && !slices.Contains(decisions, "regression") && !slices.Contains(decisions, "reverted") {
			report.UnknownPostMergeOutcomes++
		}
	}
	if slices.Contains(decisions, "closed") {
		report.ClosedPRs++
	}
	if slices.Contains(decisions, "accepted") {
		report.UserAcceptedTasks++
	}
	if slices.Contains(decisions, "regression") {
		report.Regressions++
	}
	if slices.Contains(decisions, "reverted") {
		report.Reverts++
	}
	if !sample.Operation && !slices.Contains(decisions, "accepted") && !slices.Contains(decisions, "rejected") {
		report.UnknownAcceptanceTasks++
	}
}

func appendTaskResult(report *Report, sample Sample) {
	usage := sample.Usage
	usage.Entries = nil
	report.Results = append(report.Results, TaskResult{sample.Task.ID, sample.Task.Stage, sample.Verified, sample.Task.PR != "", sample.Decision, usage, sample.Interventions})
}

func accumulateAttemptGroups(groups map[string]*Group, seen map[string]bool, sample Sample) {
	entries := make(map[string]model.UsageEntry, len(sample.Entries))
	for _, entry := range sample.Entries {
		entries[entry.AttemptID] = entry
	}
	for _, attempt := range sample.Attempts {
		addAttemptGroup(groups, seen, sample, attempt, entries[attempt.AttemptID])
	}
}

func addAttemptGroup(groups map[string]*Group, seen map[string]bool, sample Sample, attempt model.Attempt, entry model.UsageEntry) {
	thinking := attempt.EffectiveThinking
	if thinking == nil {
		thinking = attempt.Thinking
	}
	raw, _ := json.Marshal(thinking)
	name := attempt.ResolvedModel
	if name == "" {
		name = attempt.Model
	}
	identity := []string{attempt.Provider, name, string(raw), attempt.Role, sample.Category}
	keyBytes, _ := json.Marshal(identity)
	key := string(keyBytes)
	group := groups[key]
	if group == nil {
		group = &Group{Provider: attempt.Provider, Model: name, Thinking: string(raw), Role: attempt.Role, Category: sample.Category}
		groups[key] = group
	}
	if entry.Kind == "preflight" {
		group.PreflightFailures++
		return
	}
	group.Attempts++
	if attempt.Success != nil && !*attempt.Success {
		group.FailedAttempts++
	}
	if !attempt.Finished {
		group.UnfinishedAttempts++
	}
	group.ElapsedMS += entry.ElapsedMS
	if entry.Delta == nil || entry.Delta.TotalTokens == nil {
		group.UnknownTokenAttempts++
	} else {
		group.ReportedTokens += *entry.Delta.TotalTokens
	}
	verifiedKey := key + "\x00" + sample.Task.ID
	if sample.Verified && !seen[verifiedKey] {
		group.VerifiedTasks++
		seen[verifiedKey] = true
	}
}

func orderedGroups(groups map[string]*Group) []Group {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Group, 0, len(keys))
	for _, key := range keys {
		out = append(out, *groups[key])
	}
	return out
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
	case qualityRegressed(baseline, candidate):
		result.Reason = "Quality or required intervention regressed."
	case candidate.ReportedTokens >= baseline.ReportedTokens:
		result.Reason = "Reported consumption did not improve."
	default:
		result.Eligible = true
		result.Reason = "Reported consumption improved under these observations; inspect acceptance coverage and task comparability before enabling the optimization."
	}
	return result
}

func qualityRegressed(baseline, candidate Report) bool {
	return acceptanceRegressed(baseline, candidate) || operationalRegressed(baseline, candidate)
}

func acceptanceRegressed(baseline, candidate Report) bool {
	return candidate.VerifiedTasks < baseline.VerifiedTasks || candidate.UserAcceptedTasks < baseline.UserAcceptedTasks
}

func operationalRegressed(baseline, candidate Report) bool {
	return candidate.Regressions > baseline.Regressions || candidate.Reverts > baseline.Reverts || candidate.Interventions > baseline.Interventions
}
