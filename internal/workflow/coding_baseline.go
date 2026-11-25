package workflow

import (
	"context"
	"errors"
	"strings"

	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func (r *codingCheckRun) phaseAgainstBaseline(ctx context.Context, origin codingOrigin, baseline verification.Report) (string, error) {
	comparison := verification.CompareBaseline(baseline, r.report)
	r.gatePassed = comparison.Passed
	r.entry.BaselineAccepted = !r.result.Passed && comparison.Passed
	r.entry.ValidationError = ""
	if tddPhase(origin, r.entry.Production, r.entry.Tests, r.result, !comparison.Passed) == "red" {
		return "red", nil
	}
	if !r.gatePassed {
		r.setValidationError(r.blockedRedOrBaseline(origin, comparison))
		return "invalid", nil
	}
	reason, err := r.baselineRegressionDiagnostic(ctx, baseline)
	if err != nil {
		return "invalid", err
	}
	if reason != "" {
		r.setValidationError(reason)
		return "invalid", nil
	}
	return "green", nil
}

func (r *codingCheckRun) setValidationError(reason string) {
	r.entry.ValidationError = string(store.Prefix([]byte(reason), 4096))
}

func (r *codingCheckRun) blockedRedOrBaseline(origin codingOrigin, comparison verification.Report) string {
	if !r.result.Passed {
		if err := verification.StructuredTestFailureError(r.result); err != nil {
			// Preserve the original runner diagnostic too, for example if no
			// child exit status was available because sandbox setup failed.
			if r.result.Error != "" && r.result.Error != "exit status 1" && !strings.Contains(err.Error(), r.result.Error) {
				return "cannot establish a test-first red: " + err.Error() + "; runner: " + r.result.Error
			}
			return "cannot establish a test-first red: " + err.Error()
		}
		if r.entry.Production != origin.Production {
			return "cannot establish a test-first red: production files changed before the failing check"
		}
		if r.entry.Tests == origin.Tests {
			return "cannot establish a test-first red: no test file changed before the failing check"
		}
	}
	for _, result := range comparison.Results {
		if !result.Passed && result.Error != "" {
			return result.Error
		}
	}
	return "configured baseline comparison did not pass"
}

// A check can establish green only when all newly introduced regression tests
// from its latest matching red explicitly passed and no new failures remain.
func (r *codingCheckRun) baselineRegressionDiagnostic(ctx context.Context, baseline verification.Report) (string, error) {
	if len(baseline.Results) != 1 {
		return "expected exactly one matching baseline check", nil
	}
	rows, err := r.attempt.Store.List(ctx, "tdd_checks")
	if err != nil {
		return "", err
	}
	for i := len(rows) - 1; i >= 0; i-- {
		prior, err := store.Decode[tddCheck](rows[i])
		if err != nil {
			return "", err
		}
		if !r.matchesRed(prior) {
			continue
		}
		return r.pairedRegressionDiagnostic(ctx, prior, baseline.Results[0])
	}
	return "no matching test-first red for this plan version, unit, check, and test snapshot with changed production", nil
}

func (r *codingCheckRun) pairedRegressionDiagnostic(ctx context.Context, prior tddCheck, baseline verification.Result) (string, error) {
	if prior.RawResult == nil || prior.OutputArtifact == "" {
		return "recorded red check is missing its raw result or output artifact", nil
	}
	red, err := r.redCheckResult(ctx, prior)
	if errors.Is(err, store.ErrNotFound) {
		return "recorded red check output artifact is unavailable", nil
	}
	if err != nil {
		return "", err
	}
	if err := verification.RegressionEvidenceError(baseline, red, r.result); err != nil {
		return err.Error(), nil
	}
	return "", nil
}

func (r *codingCheckRun) redCheckResult(ctx context.Context, check tddCheck) (verification.Result, error) {
	metadata, output, err := r.attempt.Store.Artifact(ctx, check.OutputArtifact)
	if err != nil {
		return verification.Result{}, err
	}
	result := *check.RawResult
	result.Output = string(output)
	result.Truncated = result.Truncated || metadata.Truncated
	return result, nil
}
