package workflow

import (
	"encoding/json"
	"fmt"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

// Reviewers receive host records, never a coding agent's claim that tests ran.
func (step *taskStep) reviewExecutionEvidence() (string, error) {
	record, err := step.Store.Get(step.ctx, "verification", step.task.VerificationID)
	if err != nil {
		return "", err
	}
	var checks struct {
		Provenance model.Provenance    `json:"provenance"`
		Stage      string              `json:"stage"`
		Report     verification.Report `json:"report"`
		Baseline   verification.Report `json:"baseline"`
	}
	if err := json.Unmarshal(record.Data, &checks); err != nil {
		return "", err
	}
	checks.Report = compactReviewReport(checks.Report)
	checks.Baseline = compactReviewReport(checks.Baseline)
	pairs, _, err := step.loadTDDEvidence(step.ctx, step.task)
	if err != nil {
		return "", err
	}
	var phases []map[string]any
	for _, pair := range pairs {
		for _, check := range []tddCheck{pair.red, pair.green} {
			phase, err := step.reviewTDDPhase(check)
			if err != nil {
				return "", err
			}
			phases = append(phases, phase)
		}
	}
	return jsonText(map[string]any{"verification_id": record.ID, "checks": checks, "tdd_phases": phases}), nil
}

func compactReviewReport(report verification.Report) verification.Report {
	for i := range report.Results {
		report.Results[i].Output = reviewOutputExcerpt(report.Results[i].Output)
	}
	return report
}

func reviewOutputExcerpt(output string) string {
	if len(output) <= 4096 {
		return output
	}
	return output[:2048] + "\n[output excerpt; full output retained in host artifact]\n" + output[len(output)-2048:]
}

func (step *taskStep) reviewTDDPhase(check tddCheck) (map[string]any, error) {
	artifact, output, err := step.Store.Artifact(step.ctx, check.OutputArtifact)
	if err != nil {
		return nil, fmt.Errorf("TDD execution log unavailable: %w", err)
	}
	check.Files = nil
	return map[string]any{"phase": check, "artifact": artifact, "output_excerpt": reviewOutputExcerpt(store.RedactText(string(output)))}, nil
}
