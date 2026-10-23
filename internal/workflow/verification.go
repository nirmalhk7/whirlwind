package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

// verify reserves capacity, checks diff scope, runs baseline and risk-specific checks, and stores diagnostics in the task handoff.
func (e *Engine) verify(ctx context.Context, p model.Project, t *model.Task) (resultErr error) {
	if err := e.acquire(ctx, p); err != nil {
		return err
	}
	defer e.release(p.ID)
	checkCtx, finishBudget, budgetErr := e.verificationBudget(ctx, p, *t)
	if budgetErr != nil {
		return budgetErr
	}
	ctx = checkCtx
	defer func() { resultErr = errors.Join(resultErr, finishBudget()) }()
	run, err := e.startVerification(ctx, p, t)
	if err != nil {
		return err
	}
	files, patch, err := checkedDiff(ctx, p.Policy, t)
	if err != nil {
		run.value["stage"] = "failed"
		run.value["error"] = err.Error()
		return errors.Join(err, run.save())
	}
	return run.checkDiff(p.Policy, files, patch)
}

func (run *verificationRun) checkDiff(p model.Policy, files, patch string) error {
	sum := sha256.Sum256([]byte(patch))
	run.value["diff_hash"] = hex.EncodeToString(sum[:])
	run.value["files"] = strings.Fields(files)
	run.owner = store.ArtifactOwner{Kind: "sessions", ID: run.task.LastAttemptID}
	if run.owner.ID == "" {
		run.owner = store.ArtifactOwner{Kind: "verification", ID: run.id}
	}
	artifact, err := run.engine.Store.CaptureResource(run.ctx, run.owner, "reviewed_patch", "text/x-diff", []byte(patch), store.CaptureOptions{Limit: store.PatchPayloadLimit})
	if err != nil {
		return fmt.Errorf(verificationPersistenceError, store.ErrPersistence, err)
	}
	run.value["patch_artifact"] = artifact.ID
	risk, checks := verification.Select(p, strings.Split(files, "\n"), patch)
	run.task.Risk = risk
	run.value["risk"] = risk
	checks = structuredChecks(checks)
	baseline, err := run.engine.qualityBaseline(run.ctx, model.Project{ID: run.task.ProjectID, Path: run.task.Worktree, Policy: p, PolicyHash: evidenceDigest(p)}, *run.task, checks)
	if err != nil {
		return err
	}
	run.value["baseline"] = baseline
	checks = append(checks, model.Check{Name: "diff", Command: []string{"git", "diff", "--check", run.task.Base + "...HEAD"}}, model.Check{Name: "tracked-files-clean", Command: []string{"git", "diff", "--exit-code"}}, model.Check{Name: "index-clean", Command: []string{"git", "diff", "--cached", "--exit-code"}})
	stored := verification.Report{Passed: false, Results: []verification.Result{}}
	report, observeErr := run.runChecks(p, checks, nil)
	if observeErr == nil {
		report = verification.CompareBaseline(baseline, report)
	}
	for _, result := range report.Results {
		if err := run.captureResult(&stored, result); err != nil {
			return err
		}
	}
	return run.finish(stored, report, observeErr)
}

func (run *verificationRun) captureResult(stored *verification.Report, result verification.Result) error {
	if result.ReusedFrom != "" && result.OutputArtifact != "" {
		stored.Results = append(stored.Results, result)
		run.value["report"] = *stored
		return run.save()
	}
	artifact, err := run.engine.Store.CaptureResource(context.WithoutCancel(run.ctx), run.owner, "check:"+result.Name, "text/plain", []byte(result.Output), store.CaptureOptions{Limit: store.MessagePayloadLimit})
	if err != nil {
		return fmt.Errorf(verificationPersistenceError, store.ErrPersistence, err)
	}
	result.OutputArtifact = artifact.ID
	result.Output = string(store.Prefix([]byte(result.Output), 4096))
	result.Truncated = result.Truncated || artifact.Truncated
	stored.Results = append(stored.Results, result)
	run.value["report"] = *stored
	return run.save()
}

func (run *verificationRun) finish(stored, report verification.Report, observeErr error) error {
	stored.Passed = report.Passed
	run.value["report"] = stored
	run.value["stage"] = "complete"
	if !report.Passed || observeErr != nil {
		run.value["stage"] = "failed"
	}
	run.value["finished_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if observeErr != nil {
		run.value["error"] = observeErr.Error()
	}
	after, headErr := github.Run(run.ctx, run.task.Worktree, "git", "rev-parse", "HEAD")
	if headErr != nil || after != run.head {
		stored.Passed = false
		report.Passed = false
		run.value["report"] = stored
		run.value["stage"] = "failed"
		if headErr != nil {
			observeErr = errors.Join(observeErr, headErr)
		} else {
			observeErr = errors.Join(observeErr, errors.New("checks changed verified commit"))
		}
		run.value["error"] = observeErr.Error()
	}
	if err := run.save(); err != nil {
		return err
	}
	if observeErr != nil {
		return observeErr
	}
	return run.handoff(report)
}

func (run *verificationRun) handoff(report verification.Report) error {
	run.task.Handoff.FailingTests = nil
	for _, result := range report.Results {
		if !result.Passed {
			run.task.Handoff.FailingTests = append(run.task.Handoff.FailingTests, result.Name+": "+string(store.Prefix([]byte(result.Output), 4096))+" "+result.Error)
		}
	}
	if !report.Passed {
		return errors.New(strings.Join(run.task.Handoff.FailingTests, "\n"))
	}
	return nil
}

// progress measures check/blocker/criteria outcomes; patch churn alone never resets recovery.
func (e *Engine) progress(ctx context.Context, t *model.Task) error {
	diff, err := github.Run(ctx, t.Worktree, "git", "diff", t.Base)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(diff))
	fingerprint := hex.EncodeToString(sum[:])
	t.Fingerprint = fingerprint
	improved, err := e.measuredProgress(ctx, t)
	if err != nil {
		return err
	}
	if improved {
		t.UnchangedFailures = 0
		t.Recoveries = 0
	} else {
		t.UnchangedFailures++
	}
	if t.UnchangedFailures >= 5 {
		t.UnchangedFailures = 0
		t.Recoveries++
		t.Handoff.NextAction = "recover with new context or another provider"
		if t.Recoveries >= 3 {
			t.ResumeStage = t.Stage
			t.Stage = "paused"
			t.Error = "Three unsuccessful stall recoveries; user decision required"
			_ = e.Store.Notify(ctx, t.ID, "stall", t.Error)
		}
	}
	return nil
}

func checkedDiff(ctx context.Context, p model.Policy, t *model.Task) (string, string, error) {
	revisionRange := t.Base + "...HEAD"
	files, err := github.Run(ctx, t.Worktree, "git", "diff", "--name-only", revisionRange)
	if err != nil {
		return "", "", err
	}
	for _, file := range strings.Split(files, "\n") {
		if file != "" && !policy.AllowedPath(p, file) {
			return "", "", fmt.Errorf("scope check failed: %s", file)
		}
	}
	patch, err := github.Run(ctx, t.Worktree, "git", "diff", revisionRange)
	if err != nil {
		return "", "", err
	}
	return files, patch, nil
}
