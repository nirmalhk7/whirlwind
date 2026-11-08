package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) measuredProgress(ctx context.Context, t *model.Task) (bool, error) {
	failing, err := e.progressFailingChecks(ctx, t)
	if err != nil {
		return false, err
	}
	blockers, err := e.progressBlockingFindings(ctx, t)
	if err != nil {
		return false, err
	}
	criteria := completedCriteria(t)
	observed := model.ProgressEvidence{ChecksKnown: t.VerificationID != "" || len(t.Handoff.FailingTests) > 0 || t.ChecksPassed, ReviewKnown: t.ReviewID != "", PlanVersion: t.PlanVersion, FailingChecks: len(failing), BlockingFindings: len(blockers), CompletedCriteria: len(criteria)}
	improved := progressImproved(t.Progress, observed)
	t.Progress = mergeProgress(t.Progress, observed)
	evidence := map[string]any{"task": t.ID, "plan_version": t.PlanVersion, "observed": observed, "improved": improved, "diff_fingerprint": t.Fingerprint}
	return improved, e.Store.Put(ctx, "progress", store.ID(), evidence, 0)
}

func (e *Engine) progressFailingChecks(ctx context.Context, t *model.Task) (map[string]bool, error) {
	failing := map[string]bool{}
	for _, failure := range t.Handoff.FailingTests {
		name, _, _ := strings.Cut(failure, ":")
		failing[name] = true
	}
	if t.VerificationID != "" {
		record, err := e.Store.Get(ctx, "verification", t.VerificationID)
		if err != nil {
			return nil, err
		}
		var value struct {
			Report struct {
				Results []struct {
					Name   string `json:"name"`
					Passed bool   `json:"passed"`
				} `json:"results"`
			} `json:"report"`
		}
		if err = json.Unmarshal(record.Data, &value); err != nil {
			return nil, err
		}
		failing = map[string]bool{}
		for _, check := range value.Report.Results {
			if !check.Passed {
				failing[check.Name] = true
			}
		}
	}
	return failing, nil
}

func (e *Engine) progressBlockingFindings(ctx context.Context, t *model.Task) (map[string]bool, error) {
	blockers := map[string]bool{}
	if t.ReviewID != "" {
		record, err := e.Store.Get(ctx, "reviews", t.ReviewID)
		if err != nil {
			return nil, err
		}
		review, err := store.Decode[model.Review](record)
		if err != nil {
			return nil, err
		}
		for _, finding := range review.Findings {
			if finding.Blocking && !finding.Resolved {
				blockers[finding.ID+finding.File+finding.Message] = true
			}
		}
	}
	return blockers, nil
}

func completedCriteria(t *model.Task) map[string]bool {
	criteria := map[string]bool{}
	for _, criterion := range t.Handoff.CompletedCriteria {
		criteria[criterion] = true
	}
	return criteria
}

func progressImproved(prior *model.ProgressEvidence, observed model.ProgressEvidence) bool {
	if prior == nil || prior.PlanVersion != observed.PlanVersion {
		return false
	}
	return checksImproved(prior, observed) || reviewOrCriteriaImproved(prior, observed)
}

func checksImproved(prior *model.ProgressEvidence, observed model.ProgressEvidence) bool {
	return bothCheckStatesKnown(prior, observed) && observed.FailingChecks < prior.FailingChecks
}

func bothCheckStatesKnown(prior *model.ProgressEvidence, observed model.ProgressEvidence) bool {
	return observed.ChecksKnown && prior.ChecksKnown
}

func reviewOrCriteriaImproved(prior *model.ProgressEvidence, observed model.ProgressEvidence) bool {
	return reviewImproved(prior, observed) || observed.CompletedCriteria > prior.CompletedCriteria
}

func reviewImproved(prior *model.ProgressEvidence, observed model.ProgressEvidence) bool {
	return bothReviewStatesKnown(prior, observed) && observed.BlockingFindings < prior.BlockingFindings
}

func bothReviewStatesKnown(prior *model.ProgressEvidence, observed model.ProgressEvidence) bool {
	return observed.ReviewKnown && prior.ReviewKnown
}

func mergeProgress(prior *model.ProgressEvidence, observed model.ProgressEvidence) *model.ProgressEvidence {
	if prior == nil || prior.PlanVersion != observed.PlanVersion {
		return &observed
	}
	best := *prior
	if observed.ChecksKnown {
		if !best.ChecksKnown || observed.FailingChecks < best.FailingChecks {
			best.FailingChecks = observed.FailingChecks
		}
		best.ChecksKnown = true
	}
	if observed.ReviewKnown {
		if !best.ReviewKnown || observed.BlockingFindings < best.BlockingFindings {
			best.BlockingFindings = observed.BlockingFindings
		}
		best.ReviewKnown = true
	}
	best.CompletedCriteria = max(best.CompletedCriteria, observed.CompletedCriteria)
	return &best
}
