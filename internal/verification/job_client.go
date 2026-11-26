package verification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/process"
)

// RunLocalJob sends only the frozen manifest to a distinct local executable.
// Ambient credentials and database configuration are not inherited.
func RunLocalJob(ctx context.Context, binary string, manifest JobManifest, observe func(JobEvent) error) (JobReport, error) {
	if err := manifest.Validate(); err != nil {
		return JobReport{}, err
	}
	if err := os.MkdirAll(manifest.Root, 0700); err != nil {
		return JobReport{}, err
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return JobReport{}, err
	}
	if len(raw) > 1<<20 {
		return JobReport{}, errors.New("verification manifest exceeds 1 MiB")
	}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	child := process.Command(run, binary)
	env := verificationRunnerEnvironment(manifest.Root, child.Env)
	if manifest.OfflineQltyConfiguration != "" {
		if tools := os.Getenv("WHIRLWIND_QLTY_PROBE_TOOLS"); tools != "" {
			env = append(env, "WHIRLWIND_QLTY_PROBE_TOOLS="+tools)
		}
	}
	child.Env = env
	child.Stdin = bytes.NewReader(raw)
	child.Cancel = func() error { return child.Process.Signal(syscall.SIGTERM) }
	child.WaitDelay = 5 * time.Second
	reader, writer, err := os.Pipe()
	if err != nil {
		return JobReport{}, err
	}
	defer func() { _ = reader.Close() }()
	child.Stdout = writer
	var diagnostic capped
	child.Stderr = &diagnostic
	if err := process.Start(child); err != nil {
		_ = writer.Close()
		return JobReport{}, fmt.Errorf("start local verification runner: %w", err)
	}
	_ = writer.Close()
	done := make(chan error, 1)
	go func() {
		failure := process.Wait(child)
		cleanup := process.Reap(filepath.Join(manifest.Root, "processes"))
		done <- errors.Join(failure, cleanup)
	}()
	complete, streamErr := readJobProgress(reader, manifest.Digest(), observe)
	if streamErr != nil {
		cancel()
		_ = reader.Close()
	}
	waitErr := <-done
	if streamErr != nil || waitErr != nil || !complete {
		return JobReport{}, errors.Join(streamErr, waitErr, fmt.Errorf("local verification did not deliver a complete report: %s", diagnostic.String()))
	}
	return readJobReport(manifest)
}

const jobQualityIdentityConflict = "incompatible runtime identities from Qlty checks"

func (report JobReport) Validate(m JobManifest) error {
	if err := m.validateDefinition(); err != nil {
		return err
	}
	if err := report.validateManifestIdentity(m); err != nil {
		return err
	}
	passed, observedIdentity, err := report.validateCommandResults(m)
	if err != nil {
		return err
	}
	if (observedIdentity == nil) != (report.QualityIdentity == nil) || observedIdentity != nil && !model.SameQualityIdentity(observedIdentity, report.QualityIdentity) {
		return errors.New("verification report quality identity mismatch")
	}
	if report.Passed != passed {
		return errors.New("verification aggregate contradicts required command results")
	}
	return nil
}

func (report JobReport) validateManifestIdentity(m JobManifest) error {
	if report.Purpose != m.Purpose || report.ProjectID != m.ProjectID || report.OperationID != m.OperationID || report.ManifestHash != m.Digest() || report.PlanID != m.PlanID || report.PlanVersion != m.PlanVersion || report.PlanHash != m.PlanHash || report.ConfigurationHash != m.ConfigurationHash || report.CandidateCommit != m.CandidateCommit || report.CandidateTree != m.CandidateTree || len(report.Results) != len(m.Commands) {
		return errors.New("verification report identity or command coverage mismatch")
	}
	return nil
}

func (report JobReport) validateCommandResults(m JobManifest) (bool, *model.QualityIdentity, error) {
	passed := true
	var observed *model.QualityIdentity
	for index, result := range report.Results {
		if err := validateJobResult(m, index, result); err != nil {
			return false, nil, err
		}
		if baselineQlty(result.Result) {
			if err := validateJobQltyIdentity(m.Quality, result); err != nil {
				return false, nil, err
			}
			if !mergeJobQualityIdentity(&observed, result.QualityIdentity) {
				return false, nil, errors.New(jobQualityIdentityConflict)
			}
		}
		if requiredJobCommandFailed(m.Commands[index], result) {
			passed = false
		}
	}
	return passed, observed, nil
}

func validateJobQltyIdentity(quality *model.QualitySnapshot, result JobResult) error {
	if result.QualityIdentity == nil || result.QualityIdentity.Validate() != nil {
		if result.Error != "" {
			return fmt.Errorf("missing complete actual runtime identity in Qlty report: %s", result.Error)
		}
		return errors.New("missing complete actual runtime identity in Qlty report")
	}
	if quality != nil && quality.Identity != nil && !model.SameQualityIdentity(quality.Identity, result.QualityIdentity) {
		return errors.New("runtime identity from Qlty does not match approved setup")
	}
	return nil
}

func mergeJobQualityIdentity(first **model.QualityIdentity, current *model.QualityIdentity) bool {
	if current == nil {
		return true
	}
	if *first == nil {
		*first = current
		return true
	}
	return model.SameQualityIdentity(*first, current)
}

// ReadJobStreams restores bounded complete evidence for parsers/artifact capture;
// the IPC progress event itself carries only small previews and scoped paths.
func ReadJobStreams(root string, index int) (string, string, error) {
	directory, err := os.OpenRoot(root)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = directory.Close() }()
	read := func(suffix string) (string, error) {
		file, err := directory.Open(fmt.Sprintf("streams/%03d.%s", index, suffix))
		if err != nil {
			return "", err
		}
		defer func() { _ = file.Close() }()
		body, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		if err != nil {
			return "", err
		}
		if len(body) > 1<<20 {
			return "", errors.New("command stream exceeds bounded limit")
		}
		return string(body), nil
	}
	stdout, err := read("stdout")
	if err != nil {
		return "", "", err
	}
	stderr, err := read("stderr")
	return stdout, stderr, err
}

func verificationRunnerEnvironment(root string, inherited []string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "runner-home")}
	for _, key := range []string{"WHIRLWIND_NONO_CLI", "TMPDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	if value, set := os.LookupEnv("WHIRLWIND_SANDBOX"); set {
		env = append(env, "WHIRLWIND_SANDBOX="+value)
	}
	for _, entry := range inherited {
		if strings.HasPrefix(entry, "WHIRLWIND_PROCESS_TOKEN=") {
			env = append(env, entry)
		}
	}
	return env
}

func readJobProgress(reader io.Reader, manifestHash string, observe func(JobEvent) error) (bool, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 16<<20))
	complete := false
	var streamErr error
	for {
		var event JobEvent
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			streamErr = err
			break
		}
		eventComplete, err := observeJobProgress(event, manifestHash, observe)
		if err != nil {
			streamErr = err
			break
		}
		complete = complete || eventComplete
	}
	return complete, streamErr
}

func readJobReport(manifest JobManifest) (JobReport, error) {
	root, err := os.OpenRoot(manifest.Root)
	if err != nil {
		return JobReport{}, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open("report.json")
	if err != nil {
		return JobReport{}, err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	if err != nil {
		return JobReport{}, err
	}
	if len(body) > 16<<20 {
		return JobReport{}, errors.New("verification report exceeds limit")
	}
	var report JobReport
	if err := json.Unmarshal(body, &report); err != nil {
		return report, err
	}
	if err := report.Validate(manifest); err != nil {
		return report, err
	}
	return report, nil
}

func validateJobResult(m JobManifest, index int, result JobResult) error {
	command := m.Commands[index]
	if result.Name != command.Name || !slices.Equal(result.Command, command.Command) || result.ManifestHash != m.Digest() || result.ConfigurationHash != m.ConfigurationHash || result.CandidateCommit != m.CandidateCommit || result.CandidateTree != m.CandidateTree || !slices.Contains([]string{"pass", "fail", "error", "timeout"}, result.Status) {
		return errors.New("verification command identity mismatch")
	}
	if result.Status == "pass" && (!result.Passed || result.ExitCode == nil || *result.ExitCode != 0 || !result.Runtime.CleanupVerified) {
		return errors.New("verification pass lacks execution/cleanup evidence")
	}
	if result.Status != "pass" && result.Passed {
		return errors.New("verification result status contradicts success")
	}
	if m.OfflineDockerImage != "" {
		return validateOfflineDockerResult(m, index, result)
	}
	if err := validateRetainedArtifacts(m, index, result); err != nil {
		return err
	}
	if err := validateAutofixProposal(m, index, result); err != nil {
		return err
	}
	return nil
}

func observeJobProgress(event JobEvent, manifestHash string, observe func(JobEvent) error) (bool, error) {
	if event.ManifestHash != manifestHash {
		return false, errors.New("verification progress manifest mismatch")
	}
	if event.Type == "complete" {
		return true, nil
	}
	if event.Type != "started" && event.Type != "result" {
		return false, errors.New("unknown verification progress event")
	}
	if observe != nil {
		return false, observe(event)
	}
	return false, nil
}
