package workflow

import (
	"context"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func (r *codingCheckRun) phaseAgainstBaseline(ctx context.Context, origin codingOrigin, baseline verification.Report) (string, error) {
	comparison := verification.CompareBaseline(baseline, r.report)
	r.gatePassed = comparison.Passed
	r.entry.BaselineAccepted = !r.result.Passed && comparison.Passed
	phase := tddPhase(origin, r.entry.Production, r.entry.Tests, r.result, !comparison.Passed)
	if phase == "red" {
		return phase, nil
	}
	if !r.gatePassed {
		return "invalid", nil
	}
	passed, err := r.baselineRegressionGreen(ctx, baseline)
	if passed {
		return "green", err
	}
	return "invalid", err
}

// A check can establish green only when all newly introduced regression tests
// from its latest matching red explicitly passed and only baseline failures remain.
func (r *codingCheckRun) baselineRegressionGreen(ctx context.Context, baseline verification.Report) (bool, error) {
	if len(baseline.Results) != 1 {
		return false, nil
	}
	rows, err := r.attempt.Store.List(ctx, "tdd_checks")
	if err != nil {
		return false, err
	}
	for i := len(rows) - 1; i >= 0; i-- {
		prior, err := store.Decode[tddCheck](rows[i])
		if err != nil {
			return false, err
		}
		if !r.matchesRed(prior) {
			continue
		}
		return r.pairedRegressionPass(ctx, prior, baseline.Results[0])
	}
	return false, nil
}

func (r *codingCheckRun) pairedRegressionPass(ctx context.Context, prior tddCheck, baseline verification.Result) (bool, error) {
	if prior.RawResult == nil {
		return false, nil
	}
	red, err := r.redCheckResult(ctx, prior)
	if err != nil {
		return false, err
	}
	return verification.RegressionTestsPassed(baseline, red, r.result), nil
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
