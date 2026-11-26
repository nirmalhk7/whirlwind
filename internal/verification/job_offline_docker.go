package verification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

func (m JobManifest) validateOfflineDocker() error {
	if !model.IsOfflineDockerImage(m.OfflineDockerImage) || m.Launcher != "" || m.Quality != nil || m.Sandbox != nil {
		return errors.New("offline Docker verification requires a digest-pinned image and an approved check manifest without native scanner or sandbox settings")
	}
	if err := m.validateOfflineAuthority(); err != nil {
		return err
	}
	for _, check := range m.Commands {
		if err := model.ValidateOfflineDockerCheck(model.Check{Name: check.Name, Mode: check.Mode, Command: check.Command, OutputPaths: check.OutputPaths, Environment: check.Environment, ReportFormat: check.ReportFormat, Required: check.Required}); err != nil {
			return err
		}
	}
	return nil
}

func (m JobManifest) validateOfflineAuthority() error {
	if m.ProjectID == "" {
		if m.PolicyHash != "" || m.OfflineImageID != "" {
			return errors.New("standalone offline checks have no managed project qualification")
		}
		return nil
	}
	if !jobIdentifier.MatchString(m.ProjectID) || len(m.PolicyHash) != 64 || !gitObject.MatchString(m.PolicyHash) {
		return errors.New("managed offline checks require the approved project and policy identity")
	}
	if m.Purpose == ProjectSetupPurpose {
		if m.OfflineImageID != "" || m.ConfigurationHash != m.PolicyHash || m.CandidateCommit != m.BaseCommit {
			return errors.New("offline setup requires the approved policy and a single source revision")
		}
	} else if !execution.IsOfflineDockerImageID(m.OfflineImageID) {
		return errors.New("managed offline checks require the qualified image configuration ID")
	}
	return nil
}

func (r *jobRunner) prepareJobInput(ctx context.Context) (string, error) {
	if r.manifest.OfflineDockerImage == "" {
		return r.prepareCheckout(ctx)
	}
	config, err := os.MkdirTemp(r.root.Name(), "offline-client-")
	if err != nil {
		return "", err
	}
	r.offlineConfig = config
	r.offline, err = execution.OpenOfflineDocker(ctx, config, r.manifest.OfflineDockerImage)
	if err != nil {
		return "", err
	}
	if r.manifest.OfflineImageID != "" && r.manifest.OfflineImageID != r.offline.ImageID() {
		return "", errors.New("available offline image differs from qualified project image")
	}
	if err := r.pinOfflineDockerImage(); err != nil {
		return "", err
	}
	for index := range r.started {
		recovery, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := r.offline.Recover(recovery, offlineExecutionKey(r.manifest, index))
		cancel()
		if err != nil {
			return "", fmt.Errorf("offline verifier recovery failed: %w", err)
		}
	}
	return r.exportOfflineSource(ctx)
}

func (r *jobRunner) pinOfflineDockerImage() error {
	const receipt = "offline-image.json"
	raw, err := r.root.ReadFile(receipt)
	if os.IsNotExist(err) && len(r.started) == 0 {
		return writeJobJSON(r.root, receipt, r.offline.ImageID())
	}
	if err != nil {
		return err
	}
	var imageID string
	if err := json.Unmarshal(raw, &imageID); err != nil || imageID != r.offline.ImageID() {
		return errors.New("offline verifier image configuration differs from the job receipt")
	}
	return nil
}

func offlineExecutionKey(m JobManifest, index int) string {
	return fmt.Sprintf("ww-verify-%s-%03d", m.Digest(), index)
}

func (r *jobRunner) runOfflineDockerCheck(ctx context.Context, stage string, index int, command JobCommand, result JobResult, stdout, stderr *os.File) (JobResult, error) {
	started := time.Now()
	var output, diagnostics capped
	executionID := offlineExecutionKey(r.manifest, index)
	id, exit, cleaned, err := r.runOfflineDockerCommand(ctx, stage, executionID, command, &output, &diagnostics)
	result.Runtime = RuntimeIdentity{Kind: execution.OfflineDockerKind, ExecutionID: executionID, Generation: 1, SandboxID: id, ImageID: r.offline.ImageID(), CleanupVerified: cleaned}
	result.ExecutionProfile = r.manifest.Digest()
	result.ExitCode = exit
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		result.Status, result.Error = "timeout", "approved offline Docker command timeout exceeded"
	case err != nil:
		result.Error = err.Error()
	case exit == nil || !cleaned:
		result.Error = "offline verifier lacks process outcome or verified cleanup"
	case *exit != 0:
		result.Status, result.Error = "fail", fmt.Sprintf("exit status %d", *exit)
	default:
		result.Status, result.Passed = "pass", true
	}
	if _, err := stdout.Write(output.data); err != nil {
		return result, err
	}
	if _, err := stderr.Write(diagnostics.data); err != nil {
		return result, err
	}
	if err := errors.Join(stdout.Sync(), stderr.Sync()); err != nil {
		return result, err
	}
	result.StdoutSHA256, result.StderrSHA256 = streamHash(output.data), streamHash(diagnostics.data)
	r.captureCommandEvidence(ctx, stage, index, command, &result, output, diagnostics)
	result.Output, result.Diagnostics = offlinePreview(output.String()), offlinePreview(diagnostics.String())
	if result.Truncated && (result.Status == "pass" || result.Status == "fail") {
		result.Passed, result.Status, result.Error = false, "error", "offline command exceeded retained stream limits"
	}
	if r.manifest.Purpose == LinuxQltyProbePurpose && result.Passed {
		if err := validateLinuxQltyProbeOutput(output.String()); err != nil {
			result.Passed, result.Status, result.Error = false, "error", err.Error()
		}
	}
	result.DurationMS = time.Since(started).Milliseconds()
	result.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return result, nil
}

func (r *jobRunner) runOfflineDockerCommand(ctx context.Context, stage, executionID string, command JobCommand, output, diagnostics *capped) (string, *int, bool, error) {
	timeout := time.Duration(command.TimeoutSeconds) * time.Second
	if r.manifest.Purpose != LinuxQltyProbePurpose {
		return r.offline.Run(ctx, executionID, stage, command.Command, timeout, output, diagnostics)
	}
	bundle, err := r.prepareLinuxQltyBundle(ctx)
	if err != nil {
		return "", nil, false, err
	}
	return r.offline.RunQltyProbe(ctx, executionID, stage, bundle, timeout, output, diagnostics)
}

func validateOfflineDockerResult(m JobManifest, index int, result JobResult) error {
	if len(result.Artifacts) != 0 || result.Autofix != nil {
		return errors.New("offline Docker verifier result includes unsupported artifacts")
	}
	rootPath, err := filepath.EvalSymlinks(m.Root)
	if err != nil || result.StdoutPath != filepath.Join(rootPath, fmt.Sprintf("streams/%03d.stdout", index)) ||
		result.StderrPath != filepath.Join(rootPath, fmt.Sprintf("streams/%03d.stderr", index)) {
		return errors.New("offline Docker verifier stream paths differ from the job evidence root")
	}
	// Dependency blocks and interrupted starts have no complete streams.
	if result.Status == "error" && result.Runtime.Kind == "" && result.StdoutSHA256 == "" && result.StderrSHA256 == "" {
		return nil
	}
	if result.Runtime.Kind != execution.OfflineDockerKind || result.Runtime.ExecutionID != offlineExecutionKey(m, index) || result.Runtime.Generation != 1 || result.ExecutionProfile != m.Digest() {
		return errors.New("offline Docker verification runtime identity mismatch")
	}
	if (result.Status == "pass" || result.Status == "fail") && (result.Truncated || !result.Runtime.CleanupVerified || !execution.IsOfflineDockerIdentity(result.Runtime.SandboxID, result.Runtime.ImageID) || result.ExitCode == nil || *result.ExitCode < 0 || *result.ExitCode > 255 || (*result.ExitCode == 0) != (result.Status == "pass")) {
		return errors.New("offline Docker verification lacks complete command and cleanup proof")
	}
	if err := validateOfflineDockerImage(m, result.Runtime.ImageID); err != nil {
		return err
	}
	return validateOfflineDockerStreams(m, index, result)
}

func validateOfflineDockerImage(m JobManifest, runtimeImage string) error {
	root, err := os.OpenRoot(m.Root)
	if err != nil {
		return err
	}
	raw, err := root.ReadFile("offline-image.json")
	closeErr := root.Close()
	var imageID string
	if err := errors.Join(err, closeErr, json.Unmarshal(raw, &imageID)); err != nil || imageID == "" || imageID != runtimeImage || m.OfflineImageID != "" && m.OfflineImageID != imageID {
		return errors.New("offline Docker verification image evidence mismatch")
	}
	return nil
}

func validateOfflineDockerStreams(m JobManifest, index int, result JobResult) error {
	stdout, stderr, err := ReadJobStreams(m.Root, index)
	if err != nil {
		return err
	}
	if streamHash([]byte(stdout)) != result.StdoutSHA256 || streamHash([]byte(stderr)) != result.StderrSHA256 ||
		result.Output != offlinePreview(stdout) || result.Diagnostics != offlinePreview(stderr) {
		return errors.New("offline Docker verification streams differ from retained evidence")
	}
	if m.Purpose == LinuxQltyProbePurpose && result.Status == "pass" {
		return validateLinuxQltyProbeOutput(stdout)
	}
	return nil
}

func offlinePreview(raw string) string {
	return strings.ToValidUTF8(raw[:min(4096, len(raw))], "\uFFFD")
}

func streamHash(p []byte) string {
	sum := sha256.Sum256(p)
	return hex.EncodeToString(sum[:])
}
