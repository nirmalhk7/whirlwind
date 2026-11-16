package workflow

import (
	"context"
	"fmt"
	"strings"

	"whirlwind/internal/model"
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
		return accepted, fmt.Errorf("no valid candidate for categories: %s", strings.Join(missing, ", ")), false, nil
	}
	return finishDirectionAttempt(accepted, newCandidates)
}

func finishDirectionAttempt(accepted, newCandidates []model.Direction) ([]model.Direction, error, bool, error) {
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
	categories map[string]bool
	accepted   []model.Direction
}

func newCandidateValidationState(accepted []model.Direction) candidateValidationState {
	state := candidateValidationState{categories: map[string]bool{}, accepted: accepted}
	for _, direction := range accepted {
		state.categories[direction.Category] = true
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
			candidate.ID = model.NewIdeaID()
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
		newCandidates = append(newCandidates, candidate)
	}
	return newCandidates, nil
}

func (e *Engine) directionRejectionReason(ctx context.Context, request directionGenerationRequest, candidate model.Direction, state candidateValidationState) (model.Direction, string, error) {
	if state.categories[candidate.Category] {
		return candidate, "category already has an accepted proposal", nil
	}
	if err := validateDirectionContent(candidate); err != nil {
		return candidate, err.Error(), nil
	}
	validated := []model.Direction{candidate}
	if err := validateCandidateEvidence(validated, request.snapshot); err != nil {
		return candidate, err.Error(), nil
	}
	candidate = validated[0]
	return candidate, "", nil
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
