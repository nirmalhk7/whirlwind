package verification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/process"
)

func (r *jobRunner) run(ctx context.Context, observe func(JobEvent) error) (JobReport, error) {
	m := r.manifest
	report := JobReport{Purpose: m.Purpose, ProjectID: m.ProjectID, OperationID: m.OperationID, ManifestHash: m.Digest(), PlanID: m.PlanID, PlanVersion: m.PlanVersion, PlanHash: m.PlanHash, ConfigurationHash: m.ConfigurationHash, CandidateCommit: m.CandidateCommit, CandidateTree: m.CandidateTree, Passed: true, Results: []JobResult{}}
	stage, err := r.prepareCheckout(ctx)
	if err != nil {
		return report, err
	}
	byName := map[string]JobResult{}
	for index, command := range m.Commands {
		result, exists := r.results[index]
		if !exists {
			reason := r.blockedCommandReason(index, command, byName)
			result, err = r.command(ctx, stage, index, command, reason, observe)
			if err != nil {
				return report, err
			}
			event := JobEvent{Type: "result", ManifestHash: m.Digest(), Index: index, Result: &result}
			if err := r.record(event, observe); err != nil {
				return report, err
			}
			r.results[index] = result
		}
		byName[command.Name] = result
		report.Results = append(report.Results, result)
		if requiredJobCommandFailed(command, result) {
			report.Passed = false
		}
	}
	if err := report.Validate(m); err != nil {
		return report, err
	}
	if err := writeJobJSON(r.root, "report.json", report); err != nil {
		return report, err
	}
	return report, nil
}

func (r *jobRunner) command(ctx context.Context, stage string, index int, command JobCommand, blocked string, observe func(JobEvent) error) (JobResult, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result := JobResult{Result: Result{Name: command.Name, Command: append([]string{}, command.Command...), TimeoutSeconds: command.TimeoutSeconds, StartedAt: now, FinishedAt: now}, Status: "error", ManifestHash: r.manifest.Digest(), CandidateCommit: r.manifest.CandidateCommit, CandidateTree: r.manifest.CandidateTree, ConfigurationHash: r.manifest.ConfigurationHash}
	if err := r.root.MkdirAll("streams", 0700); err != nil {
		return result, err
	}
	stdoutName, stderrName := fmt.Sprintf("streams/%03d.stdout", index), fmt.Sprintf("streams/%03d.stderr", index)
	result.StdoutPath, result.StderrPath = filepath.Join(r.root.Name(), stdoutName), filepath.Join(r.root.Name(), stderrName)
	// Interrupted commands retain their original partial diagnostics.
	flags := os.O_CREATE | os.O_WRONLY
	if !r.started[index] {
		flags |= os.O_TRUNC
	}
	stdout, err := r.root.OpenFile(stdoutName, flags, 0600)
	if err != nil {
		return result, err
	}
	defer func() { _ = stdout.Close() }()
	stderr, err := r.root.OpenFile(stderrName, flags, 0600)
	if err != nil {
		return result, err
	}
	defer func() { _ = stderr.Close() }()
	if blocked != "" {
		result.Error = blocked
		return result, nil
	}
	if err := r.record(JobEvent{Type: "started", ManifestHash: r.manifest.Digest(), Index: index}, observe); err != nil {
		return result, err
	}
	r.started[index] = true
	return r.executeCommand(ctx, stage, index, command, result, stdout, stderr)
}

func (r *jobRunner) executeCommand(ctx context.Context, stage string, index int, command JobCommand, result JobResult, stdout, stderr *os.File) (JobResult, error) {
	frozenStage := stage
	if command.Mode == "autofix" {
		var err error
		stage, err = r.prepareFixCheckout(ctx, index)
		if err != nil {
			result.Error = err.Error()
			return result, nil
		}
	}
	started := time.Now()
	run, cancel := context.WithTimeout(ctx, time.Duration(command.TimeoutSeconds)*time.Second)
	defer cancel()
	profile, err := r.commandProfile(stage, command)
	if err == nil {
		profile.RuntimeDir = filepath.Join(r.root.Name(), "runtime", command.Name)
		if createErr := os.MkdirAll(profile.RuntimeDir, 0700); createErr != nil {
			return result, createErr
		}
		keys := make([]string, 0, len(command.Environment))
		for key := range command.Environment {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		profile.Environment = append(profile.Environment, keys...)
		profile, err = execution.PinRuntime(run, profile, r.manifest.JobID+"-"+command.Name, 1, time.Now().Add(time.Duration(command.TimeoutSeconds)*time.Second).UTC().Format(time.RFC3339Nano), filepath.Join(r.root.Name(), "executions"))
	}
	var out, diagnostics capped
	if err == nil {
		profile, err = executeJobProcess(run, stage, command, profile, &out, &diagnostics)
	}
	classifyJobCommand(&result, run, err, profile, diagnostics)
	if _, err := stdout.Write(out.data); err != nil {
		return result, err
	}
	if _, err := stderr.Write(diagnostics.data); err != nil {
		return result, err
	}
	if err := errors.Join(stdout.Sync(), stderr.Sync()); err != nil {
		return result, err
	}
	r.captureCommandEvidence(ctx, stage, index, command, &result, out, diagnostics)
	result.DurationMS = time.Since(started).Milliseconds()
	result.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	r.finishCommand(ctx, frozenStage, profile, &result)
	return result, nil
}

func (r *jobRunner) blockedCommandReason(index int, command JobCommand, byName map[string]JobResult) string {
	reason := ""
	if r.started[index] {
		reason = "interrupted command: effects uncertain; command was not replayed"
	}
	for _, dependency := range command.DependsOn {
		if byName[dependency].Status != "pass" || byName[dependency].Autofix != nil {
			reason = "dependency " + dependency + " did not pass"
			break
		}
	}
	return reason
}

func (r *jobRunner) commandProfile(stage string, command JobCommand) (execution.Profile, error) {
	profile, err := execution.ForPolicy(stage, true, r.manifest.ConfigurationHash, r.manifest.Sandbox)
	if err == nil {
		profile.Launcher = r.manifest.Launcher
		if !execution.IsNonoLauncher(profile.Launcher) {
			err = errors.New("local verifier requires native nono launcher")
		}
	}
	if err == nil {
		profile, err = execution.ForCheck(profile, command.OutputPaths)
		if err == nil && command.Mode == "autofix" {
			profile.ReadOnly = false
			profile.WriteRoots = append(profile.WriteRoots, stage)
		}
	}
	return profile, err
}

func executeJobProcess(run context.Context, stage string, command JobCommand, profile execution.Profile, out, diagnostics *capped) (execution.Profile, error) {
	profile, err := execution.ForCommand(profile, command.Command)
	if err != nil {
		return profile, err
	}
	child, err := execution.Command(run, command.Command, profile)
	if err != nil {
		return profile, err
	}
	child.Dir = stage
	for key, value := range command.Environment {
		child.Env = append(child.Env, key+"="+value)
	}
	child.Stdout, child.Stderr = out, diagnostics
	return profile, process.Run(child)
}

func classifyJobCommand(result *JobResult, run context.Context, err error, profile execution.Profile, diagnostics capped) {
	result.Passed = err == nil
	setCheckExitCode(&result.Result, err)
	result.ExecutionProfile = profile.Digest()
	switch {
	case errors.Is(run.Err(), context.DeadlineExceeded):
		result.Status = "timeout"
	case err == nil:
		result.Status = "pass"
	case strings.HasPrefix(diagnostics.String(), "nono execution failed:") || strings.HasPrefix(diagnostics.String(), "Sandbox state initialization failed:"):
		result.Status = "error"
	case result.ExitCode != nil && *result.ExitCode >= 0:
		result.Status = "fail"
	default:
		result.Status = "error"
	}
	if err != nil {
		result.Error = err.Error()
	}
	if result.Status == "timeout" {
		result.Error = "approved command timeout exceeded"
	}
}

func (r *jobRunner) captureCommandEvidence(ctx context.Context, stage string, index int, command JobCommand, result *JobResult, out, diagnostics capped) {
	result.Output = string(out.data[:min(len(out.data), 4096)])
	result.Diagnostics = string(diagnostics.data[:min(len(diagnostics.data), 4096)])
	result.Truncated = out.total > out.Len() || diagnostics.total > diagnostics.Len()
	if result.Status == "pass" || result.Status == "fail" {
		if err := validateJobEvidence(command.ReportFormat, out.data, out.total > out.Len(), result.Passed); err != nil {
			result.Passed, result.Status, result.Error = false, "error", err.Error()
		}
	}
	artifacts, artifactErr := r.captureArtifacts(stage, index, command.OutputPaths)
	result.Artifacts = artifacts
	if artifactErr != nil {
		result.Passed, result.Status, result.Error = false, "error", artifactErr.Error()
	}
	if command.Mode == "autofix" {
		proposal, err := r.captureAutofix(context.WithoutCancel(ctx), stage, index, command)
		if err != nil {
			result.Passed, result.Status, result.Error = false, "error", err.Error()
		} else {
			result.Autofix = proposal
		}
	}
}

func (r *jobRunner) finishCommand(ctx context.Context, frozenStage string, profile execution.Profile, result *JobResult) {
	if profile.ExecutionID != "" {
		result.Result = reconcileCheckRuntime(result.Result, profile)
		if !result.Passed && result.Status == "pass" {
			result.Status = "error"
		}
	}
	if err := r.validateFrozenCheckout(context.WithoutCancel(ctx), frozenStage); err != nil {
		result.Passed = false
		result.Status = "error"
		result.Error = "verification command changed frozen source"
	}
}

func requiredJobCommandFailed(command JobCommand, result JobResult) bool {
	return (command.Required == nil || *command.Required) && (result.Status != "pass" || result.Autofix != nil)
}
