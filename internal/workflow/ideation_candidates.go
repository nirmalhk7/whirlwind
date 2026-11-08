package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) validateDirectionAttempt(ctx context.Context, request directionGenerationRequest, directions []model.Direction) ([]model.Direction, error, bool, error) {
	accepted := append([]model.Direction{}, request.accepted...)
	state := newCandidateValidationState(accepted)
	newCandidates, err := e.filterDirectionCandidates(ctx, request, directions, state)
	if err != nil {
		return accepted, nil, false, err
	}
	if len(newCandidates) == 0 {
		missing := missingDirectionCategories(accepted)
		return accepted, fmt.Errorf("no novel candidate for categories: %s", strings.Join(missing, ", ")), false, nil
	}
	return e.finishDirectionAttempt(ctx, request, accepted, newCandidates)
}

func (e *Engine) finishDirectionAttempt(ctx context.Context, request directionGenerationRequest, accepted, newCandidates []model.Direction) ([]model.Direction, error, bool, error) {
	history := append(append([]model.Direction{}, unchangedCandidateEvidence(request.previous, request.snapshot.Evidence)...), accepted...)
	history, plans, err := e.targetedProposalHistory(ctx, request.project.ID, newCandidates, history, request.snapshot.ApprovedPlans, request.snapshot.HumanReconsiderations)
	if err != nil {
		return accepted, nil, false, err
	}
	request.snapshot.ApprovedPlans = plans
	unique, reason, duplicateCategories, err := e.reviewDirectionNovelty(ctx, request, newCandidates, history)
	if err != nil {
		return accepted, nil, false, err
	}
	newCandidates, err = e.rejectSemanticDuplicates(ctx, newCandidates, unique, reason, duplicateCategories)
	if err != nil {
		return accepted, nil, false, err
	}
	accepted = append(accepted, newCandidates...)
	return validateAcceptedDirections(accepted)
}

func validateAcceptedDirections(accepted []model.Direction) ([]model.Direction, error, bool, error) {
	missing := missingDirectionCategories(accepted)
	if len(missing) > 0 {
		return accepted, fmt.Errorf("missing categories: %s", strings.Join(missing, ", ")), false, nil
	}
	if err := validateCompleteDirections(accepted); err != nil {
		return accepted, err, false, nil
	}
	return accepted, nil, true, nil
}

type candidateValidationState struct {
	categories   map[string]bool
	titles       map[string]bool
	descriptions map[string]bool
	accepted     []model.Direction
}

func newCandidateValidationState(accepted []model.Direction) candidateValidationState {
	state := candidateValidationState{categories: map[string]bool{}, titles: map[string]bool{}, descriptions: map[string]bool{}, accepted: accepted}
	for _, direction := range accepted {
		state.categories[direction.Category] = true
		state.titles[directionKey(direction.Title)] = true
		state.descriptions[directionKey(direction.Description)] = true
	}
	return state
}

func (e *Engine) filterDirectionCandidates(ctx context.Context, request directionGenerationRequest, directions []model.Direction, state candidateValidationState) ([]model.Direction, error) {
	newCandidates := []model.Direction{}
	for _, candidate := range directions {
		candidate.ProjectID = request.project.ID
		candidate.Status = "proposed"
		candidate.Selected = false
		candidate.TaskID = ""
		candidate.RejectionReason = ""
		candidate.DeferralReason = ""
		if candidate.ID == "" {
			candidate.ID = store.ID()
		}
		candidate, reason, err := e.directionRejectionReason(ctx, request, candidate, state)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			if err := e.rejectDuplicateCandidate(ctx, candidate, reason, state.accepted); err != nil {
				return nil, err
			}
			continue
		}
		state.categories[candidate.Category] = true
		state.titles[directionKey(candidate.Title)] = true
		state.descriptions[directionKey(candidate.Description)] = true
		newCandidates = append(newCandidates, candidate)
	}
	return newCandidates, nil
}

func (e *Engine) directionRejectionReason(ctx context.Context, request directionGenerationRequest, candidate model.Direction, state candidateValidationState) (model.Direction, string, error) {
	if state.categories[candidate.Category] {
		return candidate, "category already has an accepted proposal", nil
	}
	if state.titles[directionKey(candidate.Title)] || state.descriptions[directionKey(candidate.Description)] {
		return candidate, "candidate repeats an accepted proposal title or description", nil
	}
	if err := validateDirectionContent(candidate); err != nil {
		return candidate, err.Error(), nil
	}
	validated := []model.Direction{candidate}
	if err := validateCandidateEvidence(validated, request.snapshot); err != nil {
		return candidate, err.Error(), nil
	}
	candidate = validated[0]
	reason, err := e.historicalProposalRejection(ctx, request.project.ID, candidate)
	return candidate, reason, err
}

func (e *Engine) historicalProposalRejection(ctx context.Context, project string, candidate model.Direction) (string, error) {
	old, err := e.Store.ProposalByFingerprint(ctx, project, candidate.Fingerprint)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if candidate.PredecessorID == old.ID && strings.TrimSpace(candidate.RevisitReason) != "" {
		if _, err := e.Store.PendingProposalReconsideration(ctx, project, old.ID, candidate.RevisitReason); err == nil {
			return "", nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return "", err
		}
	}
	if validIdeationRevisit(old, candidate) {
		return "", nil
	}
	reason := fmt.Sprintf("matches historical %s proposal %q", old.Status, old.Title)
	if old.RejectionReason != "" {
		reason += ": " + old.RejectionReason
	}
	return reason, nil
}

func (e *Engine) rejectDuplicateCandidate(ctx context.Context, candidate model.Direction, reason string, accepted []model.Direction) error {
	if reason == "category already has an accepted proposal" {
		for _, old := range accepted {
			if old.Fingerprint != "" && old.Fingerprint == candidate.Fingerprint {
				candidate.Fingerprint = ""
				break
			}
		}
	}
	return e.recordRejectedIdeation(ctx, candidate, reason)
}

func (e *Engine) rejectSemanticDuplicates(ctx context.Context, candidates []model.Direction, unique bool, reason string, categories []string) ([]model.Direction, error) {
	if unique {
		return candidates, nil
	}
	duplicates := semanticDuplicateCategories(candidates, categories)
	retained := []model.Direction{}
	for _, candidate := range candidates {
		if duplicates[candidate.Category] {
			if err := e.recordRejectedIdeation(ctx, candidate, "semantic novelty review: "+reason); err != nil {
				return nil, err
			}
			continue
		}
		retained = append(retained, candidate)
	}
	return retained, nil
}

func semanticDuplicateCategories(candidates []model.Direction, categories []string) map[string]bool {
	duplicates := map[string]bool{}
	for _, category := range categories {
		duplicates[category] = true
	}
	if len(duplicates) == 0 {
		for _, candidate := range candidates {
			duplicates[candidate.Category] = true
		}
	}
	return duplicates
}
