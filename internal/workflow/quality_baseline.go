package workflow

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func structuredChecks(checks []model.Check) []model.Check {
	out := slices.Clone(checks)
	for i := range out {
		out[i].Command = slices.Clone(out[i].Command)
	}
	return out
}
func (e *Engine) qualityBaseline(ctx context.Context, p model.Project, t model.ExecutionPlan, checks []model.Check) (verification.Report, error) {
	if t.IdeaID != "" && t.Plan == nil {
		record, err := e.Store.Get(ctx, "plan_versions", fmt.Sprintf("%s:%d", t.ID, t.PlanVersion))
		if err != nil {
			return verification.Report{}, err
		}
		frozen, err := store.Decode[model.PlanSpecification](record)
		if err != nil {
			return verification.Report{}, err
		}
		t.Plan = &frozen
	}
	key := qualityBaselineKey(t, p.PolicyHash, checks)
	baseChecks := scannerBaselineChecks(checks)
	if len(baseChecks) == 0 {
		return verification.Report{Passed: true, Results: []verification.Result{}}, nil
	}
	if r, err := e.Store.Get(ctx, "quality_baselines", key); err == nil {
		return store.Decode[verification.Report](r)
	} else if err != store.ErrNotFound {
		return verification.Report{}, err
	}
	report, err := e.captureQualityBaseline(ctx, p, t, baseChecks, key)
	if err != nil {
		return report, err
	}
	return report, e.persistQualityBaseline(ctx, key, report, baseChecks)
}

func scannerBaselineChecks(checks []model.Check) []model.Check {
	out := make([]model.Check, 0, len(checks))
	available := map[string]bool{}
	for _, check := range checks {
		if verification.IsQltyHeadDiffCommand(check.Command) || slices.ContainsFunc(check.DependsOn, func(name string) bool { return !available[name] }) {
			continue
		}
		available[check.Name] = true
		out = append(out, check)
	}
	return out
}

func (e *Engine) captureQualityBaseline(ctx context.Context, p model.Project, t model.ExecutionPlan, checks []model.Check, key string) (verification.Report, error) {
	path := filepath.Join(e.Root, "baselines", key)
	if err := e.worktree(ctx, p.Path, path, "codex/whirlwind-base-"+key[:20], t.Base); err != nil {
		return verification.Report{}, err
	}
	if t.IdeaID != "" {
		return e.captureLocalQualityBaseline(ctx, p, t, checks, path)
	}
	profile, err := execution.ForPolicy(path, true, p.PolicyHash, p.Policy.Sandbox)
	if err != nil {
		return verification.Report{}, err
	}
	return e.runOwnedChecks(ctx, path, checks, profile, runtimeCheckOwner{TaskID: t.ID, VerificationID: key, Role: "verify"})
}

func (e *Engine) captureLocalQualityBaseline(ctx context.Context, p model.Project, t model.ExecutionPlan, checks []model.Check, path string) (verification.Report, error) {
	baselinePlan := t
	baselinePlan.Worktree, baselinePlan.VerificationID = path, ""
	run, err := e.startVerification(ctx, p, &baselinePlan)
	if err != nil {
		return verification.Report{}, err
	}
	report, runErr := run.runChecks(p.Policy, checks, nil)
	if ctx.Err() == nil {
		run.value["report"], run.value["stage"] = report, "complete"
		if runErr != nil || !report.Passed {
			run.value["stage"] = "failed"
		}
		if runErr != nil {
			run.value["error"] = runErr.Error()
		}
		if err := run.save(); err != nil {
			return report, err
		}
	}
	return report, runErr
}

func (e *Engine) runOwnedChecks(ctx context.Context, cwd string, checks []model.Check, profile execution.Profile, owner runtimeCheckOwner) (verification.Report, error) {
	report := verification.Report{Passed: len(checks) > 0, Results: []verification.Result{}}
	for _, check := range checks {
		result, err := e.runOwnedCheck(ctx, cwd, check, profile, owner)
		if err != nil {
			return report, err
		}
		report.Results = append(report.Results, result)
		report.Passed = report.Passed && result.Passed
	}
	return report, nil
}

func (e *Engine) persistQualityBaseline(ctx context.Context, key string, report verification.Report, checks []model.Check) error {
	// Baseline tests may fail before Coding implements the requested behavior.
	// Retain completed process evidence without qualifying those tests as green.
	// Analyzer evidence must additionally be complete and comparable.
	for _, result := range report.Results {
		if !baselineResultUsable(result, checks) {
			return nil
		}
	}
	if err := e.Store.Put(ctx, "quality_baselines", key, report, 0); err != nil && err != store.ErrConflict {
		return fmt.Errorf("persist base quality evidence: %w", err)
	}
	return nil
}

func baselineResultUsable(result verification.Result, checks []model.Check) bool {
	for _, check := range checks {
		if verification.AdvisoryFailureAllowed(check, result) {
			return true
		}
	}
	if result.Autofix != nil || result.Truncated || result.ExitCode == nil || *result.ExitCode < 0 || *result.ExitCode > 1 {
		return false
	}
	if isQltyCommand(result.Command) {
		single := verification.Report{Results: []verification.Result{result}}
		return verification.CompareBaseline(single, single).Passed
	}
	return result.Error == "" || result.Error == "exit status 1"
}

func qualityBaselineKey(t model.ExecutionPlan, policyHash string, checks []model.Check) string {
	return evidenceDigest(struct{ Base, Policy, Commands, Configuration string }{t.Base, policyHash, evidenceDigest(checks), verificationConfigurationHash(t)})
}
