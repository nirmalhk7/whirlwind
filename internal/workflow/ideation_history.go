package workflow

import (
	"context"

	"whirlwind/internal/model"
)

type proposalHistoryContext struct {
	Where              string              `json:"where,omitempty"`
	HowItHelps         string              `json:"how_it_helps,omitempty"`
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
		where, benefit := "", ""
		if proposal.IdeaFields != nil {
			where, benefit = proposal.IdeaFields.Where, proposal.IdeaFields.HowItHelps
		}
		compact = append(compact, proposalHistoryContext{Where: where, HowItHelps: benefit, ID: proposal.ID, Category: proposal.Category, Problem: proposal.Problem, Mechanism: proposal.Mechanism, Title: proposal.Title, Components: proposal.Components, Evidence: proposal.Evidence, Revision: proposal.RepositoryRevision, Status: proposal.Status, Reason: reason, PredecessorID: proposal.PredecessorID, RevisitReason: proposal.RevisitReason, RelatedProposalIDs: proposal.RelatedProposalIDs, ApprovedPlanIDs: proposal.ApprovedPlanIDs})
	}
	return compact
}

func proposalHistoryDirection(proposal model.Proposal) model.Direction {
	return model.Direction{IdeaFields: proposal.IdeaFields, ID: proposal.ID, ProjectID: proposal.ProjectID, Category: proposal.Category, Problem: proposal.Problem, Mechanism: proposal.Mechanism, Components: proposal.Components, Evidence: proposal.Evidence, RepositoryRevision: proposal.RepositoryRevision, Status: proposal.Status, RejectionReason: proposal.RejectionReason, DeferralReason: proposal.DeferralReason, Title: proposal.Title, Description: proposal.Description, Fingerprint: proposal.Fingerprint, PredecessorID: proposal.PredecessorID, RevisitReason: proposal.RevisitReason, RelatedProposalIDs: proposal.RelatedProposalIDs, ApprovedPlanIDs: proposal.ApprovedPlanIDs}
}

func (e *Engine) recordRejectedIdeation(ctx context.Context, candidate model.Direction, reason string) error {
	proposal := model.Proposal{IdeaFields: candidate.IdeaFields, ID: candidate.ID, ProjectID: candidate.ProjectID, Category: candidate.Category, Problem: candidate.Problem, Mechanism: candidate.Mechanism, Components: candidate.Components, Evidence: candidate.Evidence, RepositoryRevision: candidate.RepositoryRevision, Status: "rejected", Title: candidate.Title, Description: candidate.Description, Fingerprint: candidate.Fingerprint, PredecessorID: candidate.PredecessorID, RevisitReason: candidate.RevisitReason, RelatedProposalIDs: candidate.RelatedProposalIDs, ApprovedPlanIDs: candidate.ApprovedPlanIDs}
	return e.Store.RecordProposalRejection(ctx, proposal, reason)
}
