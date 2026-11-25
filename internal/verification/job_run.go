package verification

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/process"
)

func (r *jobRunner) run(ctx context.Context, observe func(JobEvent) error) (JobReport, error) {
	m := r.manifest
	report := JobReport{Purpose: m.Purpose, ProjectID: m.ProjectID, OperationID: m.OperationID, ManifestHash: m.Digest(), PlanID: m.PlanID, PlanVersion: m.PlanVersion, PlanHash: m.PlanHash, ConfigurationHash: m.ConfigurationHash, CandidateCommit: m.CandidateCommit, CandidateTree: m.CandidateTree, Passed: true, Results: []JobResult{}}
	pending := jobCommandRun{byName: map[string]JobResult{}, observe: observe}
	if m.OfflineDockerImage != "" {
		stage, err := r.prepareJobInput(ctx)
		if err != nil {
			return report, err
		}
		pending.stage = stage
		defer func() { _ = os.RemoveAll(pending.stage) }()
	}
	for index, command := range m.Commands {
		result, err := r.obtainJobResult(ctx, index, command, &pending)
		if err != nil {
			return report, err
		}
		pending.byName[command.Name] = result
		if err := report.appendCommandResult(command, result); err != nil {
			return report, err
		}
	}
	return report, r.completeJobReport(report)
}

type jobCommandRun struct {
	stage   string
	byName  map[string]JobResult
	observe func(JobEvent) error
}

func (r *jobRunner) obtainJobResult(ctx context.Context, index int, command JobCommand, pending *jobCommandRun) (JobResult, error) {
	if result, exists := r.results[index]; exists {
		return result, nil
	}
	if pending.stage == "" {
		stage, err := r.prepareCheckout(ctx)
		if err != nil {
			return JobResult{}, err
		}
		pending.stage = stage
	}
	reason := r.blockedCommandReason(index, command, pending.byName)
	result, err := r.command(ctx, pending.stage, index, command, reason, pending.observe)
	if err != nil {
		return result, err
	}
	event := JobEvent{Type: "result", ManifestHash: r.manifest.Digest(), Index: index, Result: &result}
	if err := r.record(event, pending.observe); err != nil {
		return result, err
	}
	r.results[index] = result
	return result, nil
}

func (report *JobReport) appendCommandResult(command JobCommand, result JobResult) error {
	report.Results = append(report.Results, result)
	if !mergeJobQualityIdentity(&report.QualityIdentity, result.QualityIdentity) {
		return errors.New(jobQualityIdentityConflict)
	}
	if requiredJobCommandFailed(command, result) {
		report.Passed = false
	}
	return nil
}

func (r *jobRunner) completeJobReport(report JobReport) error {
	if err := report.Validate(r.manifest); err != nil {
		return err
	}
	if err := writeJobJSON(r.root, "report.json", report); err != nil {
		return err
	}
	if err := r.releaseCompletedStaging(report); err != nil {
		// Evidence is already valid and durable. A transient filesystem error
		// cannot change the result; reopening this journal retries cleanup.
		log.Printf("release verification staging for job %s: %v", r.manifest.JobID, err)
	}
	return nil
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
	if r.offline != nil {
		return r.runOfflineDockerCheck(ctx, stage, index, command, result, stdout, stderr)
	}
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
	profile, err := r.prepareCommandRuntime(run, stage, command)
	var quality jobQltyEvidence
	if isQltyCheckCommand(command.Command) {
		if r.manifest.Quality == nil {
			return result, errors.New("verification with Qlty requires frozen external quality configuration")
		}
		if err == nil {
			quality, err = r.preflightQlty(run, stage, command, profile)
		}
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
	if baselineQlty(result.Result) && result.Status != "error" && result.Status != "timeout" {
		r.acceptQltyIdentity(&result, out.data, profile.RuntimeDir, quality)
	}
	r.captureCommandEvidence(ctx, stage, index, command, &result, out, diagnostics)
	result.DurationMS = time.Since(started).Milliseconds()
	result.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	r.finishCommand(ctx, frozenStage, profile, &result)
	return result, nil
}

func (r *jobRunner) prepareCommandRuntime(ctx context.Context, stage string, command JobCommand) (execution.Profile, error) {
	profile, err := r.commandProfile(stage, command)
	if err != nil {
		return profile, err
	}
	profile.RuntimeDir = filepath.Join(r.root.Name(), "runtime", command.Name)
	if err := os.MkdirAll(profile.RuntimeDir, 0700); err != nil {
		return profile, err
	}
	if isQltyCheckCommand(command.Command) && r.manifest.Quality != nil && r.manifest.Quality.Identity != nil {
		if err := r.installApprovedQltyRuntime(ctx, stage, &profile); err != nil {
			return profile, err
		}
	}
	keys := slices.Sorted(maps.Keys(command.Environment))
	profile.Environment = append(profile.Environment, keys...)
	deadline := time.Now().Add(time.Duration(command.TimeoutSeconds) * time.Second).UTC().Format(time.RFC3339Nano)
	return execution.PinRuntime(ctx, profile, r.manifest.JobID+"-"+command.Name, 1, deadline, filepath.Join(r.root.Name(), "executions"))
}

func (r *jobRunner) installApprovedQltyRuntime(ctx context.Context, stage string, profile *execution.Profile) error {
	quality := r.manifest.Quality
	approved := quality.Identity
	if err := validateApprovedQltyRuntimePath(quality.Root, approved.ToolRuntimePath); err != nil {
		return err
	}
	if qualityPathsOverlap(stage, approved.ToolRuntimePath) {
		return errors.New("approved Qlty runtime path overlaps candidate source")
	}
	destination := filepath.Join(profile.RuntimeDir, "home", ".qlty", "cache", "tools")
	if err := copyApprovedQualityTree(ctx, approved.ToolRuntimePath, destination, approved.ToolRuntimeHash); err != nil {
		return fmt.Errorf("prepare approved Qlty scanner runtime: %w", err)
	}
	profile.ReadRoots = append(profile.ReadRoots, approved.ToolRuntimePath)
	return nil
}

func validateApprovedQltyRuntimePath(qualityRoot, runtime string) error {
	jobsRoot, err := filepath.EvalSymlinks(filepath.Join(qualityRoot, "jobs"))
	if err != nil {
		return errors.New("approved Qlty runtime jobs root is unavailable")
	}
	canonical, err := filepath.EvalSymlinks(runtime)
	if err != nil || canonical != runtime {
		return errors.New("approved Qlty runtime path is missing or non-canonical")
	}
	relative, err := filepath.Rel(jobsRoot, canonical)
	if err != nil || !filepath.IsLocal(relative) || filepath.Base(canonical) != "tools" {
		return errors.New("approved Qlty runtime path is outside retained quality job evidence")
	}
	return nil
}

type jobQltyEvidence struct {
	executable  string
	binaryHash  string
	catalogHash string
}

func (r *jobRunner) preflightQlty(ctx context.Context, stage string, command JobCommand, profile execution.Profile) (jobQltyEvidence, error) {
	var evidence jobQltyEvidence
	quality := r.manifest.Quality
	err := validateFrozenRuleSources(quality.Files)
	if err == nil {
		evidence.executable, evidence.binaryHash, err = resolveQltyJobBinary(stage, command)
	}
	if err == nil && quality.Identity != nil && evidence.binaryHash != quality.Identity.QltyBinarySHA256 {
		err = errors.New("executed Qlty binary differs from approved identity")
	}
	if err == nil {
		evidence.catalogHash, err = r.qualifyQltyCatalog(ctx, stage, command, profile)
	}
	return evidence, err
}

func resolveQltyJobBinary(stage string, command JobCommand) (string, string, error) {
	resolved, err := resolveJobCommand(stage, command)
	if err != nil {
		return "", "", err
	}
	binaryHash, err := hashQltyExecutable(resolved[0])
	return resolved[0], binaryHash, err
}

func (r *jobRunner) qualifyQltyCatalog(ctx context.Context, stage string, command JobCommand, profile execution.Profile) (string, error) {
	probe := JobCommand{
		Name:           command.Name + "-catalog-probe",
		Command:        []string{command.Command[0], "config", "show", "--no-upgrade-check"},
		TimeoutSeconds: min(command.TimeoutSeconds, 60),
	}
	var output, diagnostics capped
	if _, err := executeJobProcess(ctx, stage, probe, profile, &output, &diagnostics); err != nil {
		return "", fmt.Errorf("preflight check for the Qlty plugin catalog failed: %w", err)
	}
	const mismatch = "effective Qlty plugin catalog differs from approved identity; rerun project setup and approval"
	observed, err := HashQltyPluginCatalog(output.data)
	if err != nil {
		return "", errors.New(mismatch)
	}
	quality := r.manifest.Quality
	var approved string
	if quality.Identity != nil {
		approved = quality.Identity.PluginCatalogHash
	} else {
		approved, err = HashQltyPluginCatalog(quality.PluginCatalog)
	}
	if err != nil || observed != approved {
		return observed, errors.New(mismatch)
	}
	return observed, nil
}

func (r *jobRunner) acceptQltyIdentity(result *JobResult, output []byte, runtimeDir string, evidence jobQltyEvidence) {
	quality := r.manifest.Quality
	expected := quality.Identity
	if expected != nil {
		hash, err := hashQualityTree(expected.ToolRuntimePath)
		if err != nil || hash != expected.ToolRuntimeHash {
			result.Passed, result.Status = false, "error"
			result.Error = "approved Qlty scanner runtime changed during candidate execution"
		}
	}
	identity, err := captureQualityIdentityWithCatalog(string(output), runtimeDir, evidence.executable, quality, evidence.binaryHash, evidence.catalogHash)
	switch {
	case result.Status == "error":
		// Preserve the integrity error recorded above.
	case err != nil:
		result.Passed, result.Status = false, "error"
		result.Error = "quality runtime identity unavailable: " + err.Error()
	case expected != nil && !model.SameQualityIdentity(expected, identity):
		result.Passed, result.Status = false, "error"
		result.Error = "quality runtime identity changed; rerun project setup and approval"
	default:
		result.QualityIdentity = identity
	}
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
	resolvedCommand, err := resolveJobCommand(stage, command)
	if err != nil {
		return profile, err
	}
	profile, err = execution.ForCommand(profile, resolvedCommand)
	if err != nil {
		return profile, err
	}
	child, err := execution.Command(run, resolvedCommand, profile)
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
