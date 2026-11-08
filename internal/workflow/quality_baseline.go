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
		command := out[i].Command
		if len(command) > 1 && filepath.Base(command[0]) == "qlty" && (command[1] == "check" || command[1] == "smells") && !slices.Contains(command, "--sarif") {
			out[i].Command = append(command, "--sarif")
		}
		if len(command) > 1 && filepath.Base(command[0]) == "go" && command[1] == "test" && !slices.Contains(command, "-json") {
			out[i].Command = append(command, "-json")
		}
	}
	return out
}
func (e *Engine) qualityBaseline(ctx context.Context, p model.Project, t model.Task, checks []model.Check) (verification.Report, error) {
	key := evidenceDigest(struct{ Base, Policy, Commands string }{t.Base, p.PolicyHash, evidenceDigest(checks)})
	if r, err := e.Store.Get(ctx, "quality_baselines", key); err == nil {
		return store.Decode[verification.Report](r)
	} else if err != store.ErrNotFound {
		return verification.Report{}, err
	}
	report, err := e.captureQualityBaseline(ctx, p, t, checks, key)
	if err != nil {
		return report, err
	}
	return report, e.persistQualityBaseline(ctx, key, report)
}

func (e *Engine) captureQualityBaseline(ctx context.Context, p model.Project, t model.Task, checks []model.Check, key string) (verification.Report, error) {
	path := filepath.Join(e.Root, "baselines", key)
	if err := e.worktree(ctx, p.Path, path, "codex/whirlwind-base-"+key[:20], t.Base); err != nil {
		return verification.Report{}, err
	}
	profile, err := execution.ForPolicy(path, true, p.PolicyHash, p.Policy.Sandbox)
	if err != nil {
		return verification.Report{}, err
	}
	return e.runOwnedChecks(ctx, path, checks, profile, runtimeCheckOwner{TaskID: t.ID, VerificationID: key, Role: "verify"})
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

func (e *Engine) persistQualityBaseline(ctx context.Context, key string, report verification.Report) error {
	// Failed infrastructure or incomplete structured reports may recover on the
	// next attempt. Cache only evidence that can compare against itself.
	if !verification.CompareBaseline(report, report).Passed {
		return nil
	}
	if err := e.Store.Put(ctx, "quality_baselines", key, report, 0); err != nil && err != store.ErrConflict {
		return fmt.Errorf("persist base quality evidence: %w", err)
	}
	return nil
}
