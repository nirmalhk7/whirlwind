package store

import (
	"strings"

	"whirlwind/internal/model"
)

func proposalFromDirection(direction model.Direction, createdAt string) model.Proposal {
	problem, mechanism := direction.Problem, direction.Mechanism
	legacy := false
	if strings.TrimSpace(problem) == "" && strings.TrimSpace(mechanism) == "" {
		problem, legacy = direction.Description, true
	}
	components := append([]string{}, direction.Components...)
	if len(components) == 0 {
		components = append(components, direction.Scope...)
	}
	status := direction.Status
	if status == "" {
		status = "shown"
	}
	if direction.Selected {
		status = "selected"
	}
	revision := direction.RepositoryRevision
	if revision == "" {
		for _, evidence := range direction.Evidence {
			if evidence.Kind == "code" && evidence.Revision != "" {
				revision = evidence.Revision
				break
			}
		}
	}
	fingerprint := direction.Fingerprint
	if fingerprint == "" {
		fingerprint = model.ProposalFingerprint(direction.ProjectID, problem, mechanism, components)
	}
	return model.Proposal{ID: direction.ID, ProjectID: direction.ProjectID, Category: direction.Category, Problem: problem, Mechanism: mechanism, Components: components, Evidence: append([]model.EvidenceRef{}, direction.Evidence...), RepositoryRevision: revision, Status: status, RejectionReason: direction.RejectionReason, DeferralReason: direction.DeferralReason, Title: direction.Title, Description: direction.Description, Fingerprint: fingerprint, PredecessorID: direction.PredecessorID, RevisitReason: direction.RevisitReason, RelatedProposalIDs: append([]string{}, direction.RelatedProposalIDs...), ApprovedPlanIDs: append([]string{}, direction.ApprovedPlanIDs...), Legacy: legacy, CreatedAt: createdAt}
}
