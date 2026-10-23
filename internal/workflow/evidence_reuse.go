package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

type evidenceKey struct {
	Tree        string `json:"tree"`
	Base        string `json:"base"`
	Policy      string `json:"policy"`
	PlanVersion int    `json:"plan_version"`
	Command     string `json:"command"`
	Environment string `json:"environment"`
	Executable  string `json:"executable"`
}
type cachedCheck struct {
	Key            evidenceKey         `json:"key"`
	VerificationID string              `json:"verification_id"`
	FinishedAt     string              `json:"finished_at"`
	Result         verification.Result `json:"result"`
}

func (c cachedCheck) valid(key evidenceKey, now time.Time, freshness int) bool {
	at, err := time.Parse(time.RFC3339Nano, c.FinishedAt)
	return err == nil && c.Key == key && c.VerificationID != "" && c.Result.Passed && freshness > 0 && !at.After(now) && now.Sub(at) <= time.Duration(freshness)*time.Second
}
func evidenceDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (run *verificationRun) checkKey(policy model.Policy, check model.Check) (evidenceKey, bool) {
	if !check.Reusable || check.FreshnessSeconds <= 0 || len(check.Command) == 0 {
		return evidenceKey{}, false
	}
	// Shell command strings can hide untracked executables/toolchains; never reuse.
	name := check.Command[0]
	if slices.Contains([]string{"sh", "bash", "zsh", "fish", "env"}, filepath.Base(name)) {
		return evidenceKey{}, false
	}
	clean, err := github.Run(run.ctx, run.task.Worktree, "git", "status", "--porcelain", "-uall")
	if err != nil || clean != "" {
		return evidenceKey{}, false
	}
	tree, err := github.Run(run.ctx, run.task.Worktree, "git", "rev-parse", "HEAD^{tree}")
	if err != nil {
		return evidenceKey{}, false
	}
	executable, err := exec.LookPath(name)
	if err != nil {
		return evidenceKey{}, false
	}
	bytes, err := os.ReadFile(executable)
	if err != nil {
		return evidenceKey{}, false
	}
	sum := sha256.Sum256(bytes)
	env := os.Environ()
	slices.Sort(env)
	key := evidenceKey{Tree: tree, Base: run.task.Base, Policy: evidenceDigest(policy), PlanVersion: run.task.PlanVersion, Command: evidenceDigest(check), Environment: evidenceDigest([]any{runtime.GOOS, runtime.GOARCH, runtime.Version(), env}), Executable: hex.EncodeToString(sum[:])}
	return key, true
}

func (run *verificationRun) cachedResult(key evidenceKey, check model.Check) (verification.Result, bool) {
	record, err := run.engine.Store.Get(run.ctx, "check_cache", run.task.ID+":"+evidenceDigest(key))
	if err != nil {
		return verification.Result{}, false
	}
	saved, err := store.Decode[cachedCheck](record)
	if err != nil || !saved.valid(key, time.Now(), check.FreshnessSeconds) {
		return verification.Result{}, false
	}
	verificationRecord, err := run.engine.Store.Get(run.ctx, "verification", saved.VerificationID)
	if err != nil {
		return verification.Result{}, false
	}
	var completed struct {
		Stage string `json:"stage"`
	}
	if json.Unmarshal(verificationRecord.Data, &completed) != nil || completed.Stage != "complete" {
		return verification.Result{}, false
	}
	result := saved.Result
	result.ReusedFrom = saved.VerificationID
	return result, true
}
func (run *verificationRun) cacheResult(ctx context.Context, key evidenceKey, result verification.Result) error {
	if !result.Passed {
		return nil
	}
	artifact, err := run.engine.Store.CaptureResource(ctx, store.ArtifactOwner{Kind: "verification", ID: run.id}, "cached_check:"+result.Name, "text/plain", []byte(result.Output), store.CaptureOptions{Limit: store.MessagePayloadLimit})
	if err != nil {
		return err
	}
	result.OutputArtifact = artifact.ID
	result.Output = string(store.Prefix([]byte(result.Output), 4096))
	result.Truncated = result.Truncated || artifact.Truncated
	id := run.task.ID + ":" + evidenceDigest(key)
	record, err := run.engine.Store.Get(ctx, "check_cache", id)
	if err != nil && err != store.ErrNotFound {
		return err
	}
	return run.engine.Store.Put(ctx, "check_cache", id, cachedCheck{Key: key, VerificationID: run.id, FinishedAt: result.FinishedAt, Result: result}, record.Version)
}

func (run *verificationRun) runChecks(policy model.Policy, checks []model.Check, observe func(verification.Result) error) (verification.Report, error) {
	profile, err := execution.ForPolicy(run.task.Worktree, true, evidenceDigest(policy), policy.Sandbox)
	if err != nil {
		return verification.Report{}, err
	}
	report := verification.Report{Passed: len(checks) > 0, Results: []verification.Result{}}
	for _, check := range checks {
		key, cacheable := run.checkKey(policy, check)
		result, reused := run.cachedResult(key, check)
		if !cacheable {
			reused = false
		}
		if !reused {
			part, err := verification.RunObservedWithProfile(run.ctx, run.task.Worktree, []model.Check{check}, profile, nil)
			if err != nil {
				return report, err
			}
			if len(part.Results) == 0 {
				return report, fmt.Errorf("check %s returned no evidence", check.Name)
			}
			result = part.Results[0]
			// A concurrent source/toolchain change invalidates the just-produced result.
			after, stillCacheable := run.checkKey(policy, check)
			if cacheable && stillCacheable && key == after {
				if err = run.cacheResult(run.ctx, key, result); err != nil {
					return report, err
				}
			}
		}
		report.Results = append(report.Results, result)
		report.Passed = report.Passed && result.Passed
		if observe != nil {
			if err = observe(result); err != nil {
				return report, err
			}
		}
		if err = run.ctx.Err(); err != nil {
			return report, err
		}
	}
	return report, nil
}
