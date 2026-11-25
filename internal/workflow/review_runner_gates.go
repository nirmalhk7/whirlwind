package workflow

import (
	"errors"
	"fmt"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

// Recompute qualification from complete process streams and the retained base.
// The raw journal and its aggregate are never rewritten as successful execution.
func (step *taskStep) qualifiedRunnerReport(manifest verification.JobManifest, raw verification.JobReport, checks []model.Check) (verification.Report, error) {
	report, err := completeRunnerStreams(manifest, raw)
	if err != nil {
		return report, err
	}
	if !runnerChecksNeedBaseline(checks) {
		if !raw.Passed {
			return report, errors.New("runner report contains failed mandatory gates")
		}
		return report, nil
	}
	baseline, err := step.retainedRunnerBaseline(checks)
	if err != nil {
		return report, err
	}
	qualified := verification.CompareBaselineForChecks(baseline, report, checks)
	if !qualified.Passed {
		return qualified, errors.New("runner report contains failed mandatory gates")
	}
	return qualified, nil
}

func runnerChecksNeedBaseline(checks []model.Check) bool {
	for _, check := range checks {
		if verification.IsQltyHeadDiffCommand(check.Command) {
			continue
		}
		if check.ReportFormat == "sarif" || isQltyCommand(check.Command) {
			return true
		}
	}
	return false
}

func completeRunnerStreams(manifest verification.JobManifest, raw verification.JobReport) (verification.Report, error) {
	report := verification.Report{Passed: raw.Passed}
	for index, result := range raw.Results {
		owned := runtimeIdentityComplete(result.Runtime)
		if manifest.OfflineDockerImage != "" {
			owned = managedOfflineResult(manifest, result)
		}
		if result.Autofix != nil || result.Status == "error" || result.Status == "timeout" || !owned || !result.Runtime.CleanupVerified {
			return report, errors.New("runner report contains incomplete execution or pending source fixes")
		}
		value := result.Result
		var err error
		value.Output, value.Diagnostics, err = verification.ReadJobStreams(manifest.Root, index)
		if err != nil {
			return report, fmt.Errorf("runner command streams unavailable: %w", err)
		}
		if verification.IsQltyHeadDiffCommand(value.Command) {
			if err := verification.ValidateQltyResult(value); err != nil {
				return report, fmt.Errorf("runner HEAD-diff Qlty evidence rejected: %w", err)
			}
		}
		report.Results = append(report.Results, value)
	}
	return report, nil
}

func (step *taskStep) retainedRunnerBaseline(checks []model.Check) (verification.Report, error) {
	key := qualityBaselineKey(step.task, evidenceDigest(step.project.Policy), structuredChecks(checks))
	record, err := step.Store.Get(step.ctx, "quality_baselines", key)
	if err != nil {
		return verification.Report{}, fmt.Errorf("runner qualification requires retained approved baseline: %w", err)
	}
	baseline, err := store.Decode[verification.Report](record)
	if err != nil {
		return verification.Report{}, err
	}
	for _, result := range baseline.Results {
		if !runtimeIdentityComplete(result.Runtime) || !result.Runtime.CleanupVerified || result.Autofix != nil {
			return verification.Report{}, errors.New("baseline lacks complete isolated execution evidence")
		}
	}
	return baseline, nil
}
