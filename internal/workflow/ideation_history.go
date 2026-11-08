package workflow

import (
	"context"
	"errors"
	"strings"

	"whirlwind/internal/model"
)

type proposalHistoryContext struct {
	ID                 string              `json:"id"`
	Category           string              `json:"cat"`
	Problem            string              `json:"problem"`
	Mechanism          string              `json:"mechanism"`
	Title              string              `json:"title,omitempty"`
	Components         []string            `json:"components,omitempty"`
	Evidence           []model.EvidenceRef `json:"evidence,omitempty"`
	Revision           string              `json:"revision,omitempty"`
	Status             string              `json:"status,omitempty"`
	Reason             string              `json:"reason,omitempty"`
	PredecessorID      string              `json:"predecessor,omitempty"`
	RevisitReason      string              `json:"revisit,omitempty"`
	RelatedProposalIDs []string            `json:"related,omitempty"`
	ApprovedPlanIDs    []string            `json:"plans,omitempty"`
}

func compactProposalHistory(history []model.Direction) []proposalHistoryContext {
	compact := make([]proposalHistoryContext, 0, len(history))
	for _, proposal := range history {
		reason := proposal.RejectionReason
		if reason == "" {
			reason = proposal.DeferralReason
		}
		compact = append(compact, proposalHistoryContext{ID: proposal.ID, Category: proposal.Category, Problem: proposal.Problem, Mechanism: proposal.Mechanism, Title: proposal.Title, Components: proposal.Components, Evidence: proposal.Evidence, Revision: proposal.RepositoryRevision, Status: proposal.Status, Reason: reason, PredecessorID: proposal.PredecessorID, RevisitReason: proposal.RevisitReason, RelatedProposalIDs: proposal.RelatedProposalIDs, ApprovedPlanIDs: proposal.ApprovedPlanIDs})
	}
	return compact
}

// Ideate retains a generation lease while preparing, validating, and saving four directions.
func (e *Engine) targetedProposalHistory(ctx context.Context, project string, candidates, recent []model.Direction, approvedPlans []approvedPlanContext, humanRequests []model.ProposalReconsideration) ([]model.Direction, []approvedPlanContext, error) {
	targeted, err := e.collectTargetedProposalHistory(ctx, project, candidates)
	if err != nil {
		return nil, nil, err
	}
	combined := append(targeted, recent...)
	requestLimit := e.proposalHistoryRequestLimit(ctx)
	planContext, err := boundedApprovedPlanContext(approvedPlans, requestLimit/4)
	if err != nil {
		return nil, nil, err
	}
	planBytes := len(boundedSources(jsonText(planContext)))
	reconsiderationBytes := len(boundedSources(jsonText(humanRequests)))
	if reconsiderationBytes > requestLimit-planBytes {
		return nil, nil, errors.New("ideation history budget exhausted before human reconsideration context")
	}
	historyLimit := requestLimit - planBytes - reconsiderationBytes
	bounded := boundProposalHistory(combined, historyLimit, 12)
	if len(targeted) > 0 && !containsAnyProposal(bounded, targeted) {
		return nil, nil, errors.New("ideation history budget exhausted before a complete candidate comparison could be retrieved")
	}
	historyBytes := len(boundedSources(jsonText(compactProposalHistory(bounded))))
	if run := historyBudgetFrom(ctx); run != nil {
		if err := run.consume(historyBytes + planBytes + reconsiderationBytes); err != nil {
			return nil, nil, err
		}
	}
	return bounded, planContext, nil
}

func (e *Engine) collectTargetedProposalHistory(ctx context.Context, project string, candidates []model.Direction) ([]model.Direction, error) {
	limit := max(1, 12/max(1, len(candidates)))
	targeted := make([]model.Direction, 0, 12)
	seen := map[string]bool{}
	for _, candidate := range candidates {
		for _, query := range candidateProposalQueries(project, candidate, limit) {
			if err := e.appendProposalMatches(ctx, query, seen, &targeted); err != nil {
				return nil, err
			}
		}
	}
	return targeted, nil
}

func candidateProposalQueries(project string, candidate model.Direction, limit int) []model.ProposalQuery {
	terms := strings.Join([]string{candidate.Problem, candidate.Mechanism, strings.Join(candidate.Components, " ")}, " ")
	return []model.ProposalQuery{
		{ProjectID: project, Category: candidate.Category, Components: candidate.Components, Terms: terms, Limit: limit},
		// Category-neutral search catches renamed ideas that changed category.
		{ProjectID: project, Terms: terms, Limit: limit},
	}
}

func (e *Engine) appendProposalMatches(ctx context.Context, query model.ProposalQuery, seen map[string]bool, targeted *[]model.Direction) error {
	matches, err := e.Store.SearchProposals(ctx, query)
	if err != nil {
		return err
	}
	for _, proposal := range matches {
		if seen[proposal.ID] || len(*targeted) >= 12 {
			continue
		}
		seen[proposal.ID] = true
		*targeted = append(*targeted, proposalHistoryDirection(proposal))
	}
	return nil
}

func (e *Engine) proposalHistoryRequestLimit(ctx context.Context) int {
	limit := e.settings(ctx).Ideation.HistoryTokenBudget
	if limit <= 0 {
		limit = 4000
	}
	if run := historyBudgetFrom(ctx); run != nil {
		limit = run.requestLimit()
	}
	return limit
}

func boundedApprovedPlanContext(plans []approvedPlanContext, maxBytes int) ([]approvedPlanContext, error) {
	if len(plans) == 0 {
		return nil, nil
	}
	if maxBytes <= 0 {
		return nil, errors.New("ideation history budget exhausted before approved-plan comparison")
	}
	bounded := make([]approvedPlanContext, 0, len(plans))
	for _, plan := range plans {
		candidate := append(append([]approvedPlanContext{}, bounded...), plan)
		if len(boundedSources(jsonText(candidate))) > maxBytes {
			continue
		}
		bounded = candidate
	}
	if len(bounded) == 0 {
		return nil, errors.New("ideation history budget cannot fit a complete approved-plan comparison")
	}
	return bounded, nil
}

func containsAnyProposal(history, targeted []model.Direction) bool {
	ids := make(map[string]bool, len(history))
	for _, proposal := range history {
		ids[proposal.ID] = true
	}
	for _, proposal := range targeted {
		if ids[proposal.ID] {
			return true
		}
	}
	return false
}

func proposalHistoryDirection(proposal model.Proposal) model.Direction {
	return model.Direction{ID: proposal.ID, ProjectID: proposal.ProjectID, Category: proposal.Category, Problem: proposal.Problem, Mechanism: proposal.Mechanism, Components: proposal.Components, Evidence: proposal.Evidence, RepositoryRevision: proposal.RepositoryRevision, Status: proposal.Status, RejectionReason: proposal.RejectionReason, DeferralReason: proposal.DeferralReason, Title: proposal.Title, Description: proposal.Description, Fingerprint: proposal.Fingerprint, PredecessorID: proposal.PredecessorID, RevisitReason: proposal.RevisitReason, RelatedProposalIDs: proposal.RelatedProposalIDs, ApprovedPlanIDs: proposal.ApprovedPlanIDs}
}

func (e *Engine) recordRejectedIdeation(ctx context.Context, candidate model.Direction, reason string) error {
	proposal := model.Proposal{ID: candidate.ID, ProjectID: candidate.ProjectID, Category: candidate.Category, Problem: candidate.Problem, Mechanism: candidate.Mechanism, Components: candidate.Components, Evidence: candidate.Evidence, RepositoryRevision: candidate.RepositoryRevision, Status: "rejected", Title: candidate.Title, Description: candidate.Description, Fingerprint: candidate.Fingerprint, PredecessorID: candidate.PredecessorID, RevisitReason: candidate.RevisitReason, RelatedProposalIDs: candidate.RelatedProposalIDs, ApprovedPlanIDs: candidate.ApprovedPlanIDs}
	return e.Store.RecordProposalRejection(ctx, proposal, reason)
}

func validIdeationRevisit(previous model.Proposal, candidate model.Direction) bool {
	if candidate.PredecessorID != previous.ID || strings.TrimSpace(candidate.RevisitReason) == "" {
		return false
	}
	if previous.RepositoryRevision != candidate.RepositoryRevision {
		return true
	}
	return jsonText(previous.Evidence) != jsonText(candidate.Evidence)
}

// reviewDirectionNovelty checks meaning across candidates and bounded project history.
