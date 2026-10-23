package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) measuredProgress(ctx context.Context, t *model.Task) (bool, error) {
	failing := map[string]bool{}
	for _, failure := range t.Handoff.FailingTests {
		name, _, _ := strings.Cut(failure, ":")
		failing[name] = true
	}
	if t.VerificationID != "" {
		record, err := e.Store.Get(ctx, "verification", t.VerificationID)
		if err != nil {
			return false, err
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
			return false, err
		}
		failing = map[string]bool{}
		for _, check := range value.Report.Results {
			if !check.Passed {
				failing[check.Name] = true
			}
		}
	}
	blockers := map[string]bool{}
	if t.ReviewID != "" {
		record, err := e.Store.Get(ctx, "reviews", t.ReviewID)
		if err != nil {
			return false, err
		}
		review, err := store.Decode[model.Review](record)
		if err != nil {
			return false, err
		}
		for _, finding := range review.Findings {
			if finding.Blocking && !finding.Resolved {
				blockers[finding.ID+finding.File+finding.Message] = true
			}
		}
	}
	criteria := map[string]bool{}
	for _, criterion := range t.Handoff.CompletedCriteria {
		criteria[criterion] = true
	}
	observed := model.ProgressEvidence{ChecksKnown: t.VerificationID != "" || len(t.Handoff.FailingTests) > 0 || t.ChecksPassed, ReviewKnown: t.ReviewID != "", PlanVersion: t.PlanVersion, FailingChecks: len(failing), BlockingFindings: len(blockers), CompletedCriteria: len(criteria)}
	prior := t.Progress
	improved := prior != nil && prior.PlanVersion == t.PlanVersion && ((observed.ChecksKnown && prior.ChecksKnown && observed.FailingChecks < prior.FailingChecks) || (observed.ReviewKnown && prior.ReviewKnown && observed.BlockingFindings < prior.BlockingFindings) || observed.CompletedCriteria > prior.CompletedCriteria)
	if prior == nil || prior.PlanVersion != t.PlanVersion {
		t.Progress = &observed
	} else {
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
		t.Progress = &best
	}

	evidence := map[string]any{"task": t.ID, "plan_version": t.PlanVersion, "observed": observed, "improved": improved, "diff_fingerprint": t.Fingerprint}
	return improved, e.Store.Put(ctx, "progress", store.ID(), evidence, 0)
}
