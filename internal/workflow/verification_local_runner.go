package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"whirlwind/internal/execution"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func (run *verificationRun) runLocalChecks(policy model.Policy, checks []model.Check, observe func(verification.Result) error) (reportValue verification.Report, resultErr error) {
	defer func() {
		if resultErr != nil && !errors.Is(resultErr, store.ErrPersistence) {
			resultErr = errors.Join(verification.ErrJobSetup, resultErr)
		}
	}()

	task := run.task
	if task.PlanDocument == nil || task.PlanDocument.ApprovedHash == "" || task.PlanDocument.Hash != task.PlanDocument.ApprovedHash || task.PlanDocument.PolicyHash == "" {
		return verification.Report{}, errors.New("verification requires approved document and configuration identity")
	}
	binary, err := localVerifierBinary()
	if err != nil {
		return verification.Report{}, err
	}
	manifest, err := run.localCheckManifest(policy, checks)
	if err != nil {
		return verification.Report{}, err
	}
	if err := run.persistRunnerManifest(manifest); err != nil {
		return verification.Report{}, err
	}
	complete, err := verification.RunLocalJob(run.ctx, binary, manifest, func(event verification.JobEvent) error {
		return run.observeLocalCheck(manifest, observe, event)
	})
	return run.retainRunnerReport(manifest, complete, err)
}

func localVerifierBinary() (string, error) {
	binary := os.Getenv("WHIRLWIND_VERIFY_BIN")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("whirlwind-verify")
		if err != nil {
			return "", fmt.Errorf("local verifier missing; install whirlwind-verify or configure WHIRLWIND_VERIFY_BIN: %w", err)
		}
	}
	return binary, nil
}

func (run *verificationRun) localCheckManifest(policy model.Policy, checks []model.Check) (verification.JobManifest, error) {
	task := run.task
	tree, err := github.Run(run.ctx, task.Worktree, "git", "rev-parse", run.head+"^{tree}")
	if err != nil {
		return verification.JobManifest{}, err
	}
	manifest := verification.JobManifest{Version: 1, JobID: run.id, PlanID: task.ID, PlanVersion: task.PlanVersion, PlanHash: task.PlanDocument.ApprovedHash, ConfigurationHash: verificationConfigurationHash(*task), CandidateCommit: run.head, CandidateTree: tree, BaseCommit: task.Base, Repository: task.Worktree, Root: filepath.Join(run.engine.Root, "verification", run.id), Sandbox: policy.Sandbox}
	manifest.Commands = verificationJobCommands(checks)
	if policy.OfflineDockerImage != "" {
		if err := run.bindOfflineManifest(&manifest, policy); err != nil {
			return verification.JobManifest{}, err
		}
	} else {
		profile, err := execution.ForPolicy(task.Worktree, true, task.PlanDocument.PolicyHash, policy.Sandbox)
		if err != nil {
			return verification.JobManifest{}, err
		}
		if !execution.IsNonoLauncher(profile.Launcher) {
			return verification.JobManifest{}, errors.New("canonical local verification requires native nono")
		}
		manifest.Launcher = profile.Launcher
	}
	quality, err := approvedQualitySnapshot(*task)
	if err != nil {
		return verification.JobManifest{}, err
	}
	if quality != nil {
		manifest.Quality = quality
		manifest.Root = filepath.Join(quality.Root, "jobs", run.id)
	}
	if err := manifest.Validate(); err != nil {
		return verification.JobManifest{}, err
	}
	return manifest, nil
}

func (run *verificationRun) persistRunnerManifest(manifest verification.JobManifest) error {
	if saved, exists := run.value["runner_manifest"]; exists {
		raw, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		var prior verification.JobManifest
		if err := json.Unmarshal(raw, &prior); err != nil {
			return err
		}
		if prior.Digest() != manifest.Digest() {
			return errors.New("unfinished verification manifest changed; inspect original journal before starting another job")
		}
	}
	run.value["runner_manifest"], run.value["runner_manifest_hash"] = manifest, manifest.Digest()
	if err := run.save(); err != nil {
		return err
	}
	return nil
}

func (run *verificationRun) observeLocalCheck(manifest verification.JobManifest, observe func(verification.Result) error, event verification.JobEvent) error {
	run.value["runner_progress"] = event
	if event.Type == "result" && event.Result != nil {
		partial, _ := run.value["runner_partial"].([]verification.JobResult)
		run.value["runner_partial"] = append(partial, *event.Result)
		if observe != nil {
			result := event.Result.Result
			var streamErr error
			result.Output, result.Diagnostics, streamErr = verification.ReadJobStreams(manifest.Root, event.Index)
			if streamErr != nil {
				return streamErr
			}
			if err := observe(result); err != nil {
				return err
			}
		}
	}
	return run.save()
}

func (run *verificationRun) retainRunnerReport(manifest verification.JobManifest, complete verification.JobReport, err error) (verification.Report, error) {
	report := verification.Report{Passed: complete.Passed, Results: []verification.Result{}}
	if err != nil {
		return report, err
	}
	for index, result := range complete.Results {
		raw, err := run.retainRunnerResult(manifest, index, result)
		if err != nil {
			return report, err
		}
		report.Results = append(report.Results, raw)
	}
	run.value["runner_report"] = complete
	if err := run.save(); err != nil {
		return report, err
	}
	for _, result := range complete.Results {
		if result.Status == "error" && !strings.HasPrefix(result.Error, "dependency ") {
			return report, fmt.Errorf("local verification setup/recovery error in %s: %s", result.Name, result.Error)
		}
	}
	return report, nil
}

func (run *verificationRun) retainRunnerResult(manifest verification.JobManifest, index int, result verification.JobResult) (verification.Result, error) {
	raw := result.Result
	var err error
	raw.Output, raw.Diagnostics, err = verification.ReadJobStreams(manifest.Root, index)
	if err != nil {
		return raw, err
	}
	if raw.Autofix != nil {
		raw.AutofixArtifact, err = run.retainAutofix(manifest, index, result)
		if err != nil {
			return raw, err
		}
		raw.Passed = false
		raw.Error = "source autofix proposal requires a new code version and full mandatory rerun: " + raw.Autofix.PatchPath + " (sha256 " + raw.Autofix.SHA256 + ")"
	}
	return raw, nil
}
