package verification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"
)

// LinuxQltyProbePurpose is operator-only qualification. It has no approved
// project, plan or delivery authority and cannot satisfy a workflow gate.
const LinuxQltyProbePurpose = "linux_qlty_probe"

const (
	linuxQltyProbeImage   = "golang@sha256:87c65a688975c0b1d4585d204874c45ca78354670e3a5f11368c5a492303d696"
	linuxQltyProbeVersion = "0.668.0"
	linuxQltyProbeBinary  = "33bb7f1107b3a9626aaad4cf629346fd300aa6e63612075af12febdaed549d4d"
	gitleaksProbeBinary   = "2d248645545650fa16eaa61424c86bd2d22646a7205a06b5942d86e8a48c0dd1"
	linuxQltyProbeName    = "whirlwind-linux-qlty-probe"
	probeDriverSuccess    = "qlty:message:invocation.concluded.success"
)

const qltyProbeScript = `#!/bin/sh
set -eu
export HOME=/tmp/home GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null QLTY_TELEMETRY=off
mkdir -p "$HOME" /tmp/scan
cp -R /scanner/workspace/. /tmp/scan
cd /tmp/scan
exec /scanner/qlty check --sarif --no-progress --no-cache --skip-source-fetch --no-upgrade-check --no-fix --jobs 1 trigger.txt
`

// Qlty can drop Gitleaks locations outside the synthetic workspace. A Gitleaks
// findings exit must therefore be an unsuccessful invocation, never success.
const qltyProbeConfig = `config_version = "0"
[[plugin]]
name = "whirlwind-gitleaks"
version = "8.29.1"
[plugins.definitions.whirlwind-gitleaks]
file_types = ["ALL"]
security = true
[plugins.definitions.whirlwind-gitleaks.drivers.lint]
script = '''/scanner/gitleaks dir /checkout --config /scanner/gitleaks.toml --gitleaks-ignore-path /scanner/gitleaks.ignore --ignore-gitleaks-allow --redact=100 --exit-code=101 -f sarif -r ${tmpfile}'''
success_codes = [0]
output = "tmpfile"
output_format = "sarif"
output_category = "secret"
output_level = "high"
batch = false
output_missing = "parse"
`

const gitleaksProbeConfig = "[extend]\nuseDefault = true\n"
const qltyProbeTrigger = "Pinned offline scanner qualification.\n"

func linuxQltyProbeConfiguration() string {
	return streamHash([]byte(linuxQltyProbeImage + "\x00" + linuxQltyProbeVersion + "\x00" + linuxQltyProbeBinary + "\x00" + gitleaksProbeBinary +
		"\x00" + qltyProbeScript + "\x00" + qltyProbeConfig + "\x00" + gitleaksProbeConfig + "\x00" + qltyProbeTrigger))
}

func (m JobManifest) validateLinuxQltyProbe() error {
	if m.ProjectID != "" || m.PlanID != "" || m.PlanVersion != 0 || m.PlanHash != "" || m.OperationID != "" ||
		m.PolicyHash != "" || m.OfflineImageID != "" || m.ConfigurationHash != linuxQltyProbeConfiguration() {
		return errors.New("Linux Qlty probe has no project, qualification, or plan authority")
	}
	if m.OfflineDockerImage != linuxQltyProbeImage || m.Launcher != "" || m.Quality != nil || m.Sandbox != nil ||
		len(m.Commands) != 1 || m.CandidateCommit != m.BaseCommit {
		return errors.New("Linux Qlty probe requires a single exact revision and the pinned offline runtime")
	}
	check := m.Commands[0]
	if check.Name != linuxQltyProbeName || !slices.Equal(check.Command, []string{linuxQltyProbeName}) || check.TimeoutSeconds != 90 ||
		check.ReportFormat != "sarif" || check.Mode != "" || check.Required != nil ||
		len(check.DependsOn)+len(check.OutputPaths)+len(check.Environment) != 0 {
		return errors.New("Linux Qlty probe requires the fixed scanner with no additional grants")
	}
	return nil
}

// LinuxQltyProbeRequest provides the operator's exact source identities. The
// scanner runtime, project authority and executable are not caller inputs.
type LinuxQltyProbeRequest struct {
	JobID           string `json:"job_id"`
	Repository      string `json:"repository"`
	Root            string `json:"root"`
	CandidateCommit string `json:"candidate_commit"`
	CandidateTree   string `json:"candidate_tree"`
}

func (request LinuxQltyProbeRequest) Manifest() JobManifest {
	return JobManifest{
		Version: 1, Purpose: LinuxQltyProbePurpose, JobID: request.JobID,
		ConfigurationHash: linuxQltyProbeConfiguration(), OfflineDockerImage: linuxQltyProbeImage,
		Repository: request.Repository, Root: request.Root, CandidateCommit: request.CandidateCommit,
		CandidateTree: request.CandidateTree, BaseCommit: request.CandidateCommit,
		Commands: []JobCommand{{Name: linuxQltyProbeName, Command: []string{linuxQltyProbeName}, TimeoutSeconds: 90, ReportFormat: "sarif"}},
	}
}

func (r *jobRunner) prepareLinuxQltyBundle(ctx context.Context) (string, error) {
	if runtime.GOARCH != "amd64" {
		return "", errors.New("Linux Qlty probe requires the pinned amd64 toolchain")
	}
	binaries := os.Getenv("WHIRLWIND_QLTY_PROBE_TOOLS")
	if !filepath.IsAbs(binaries) {
		return "", errors.New("set WHIRLWIND_QLTY_PROBE_TOOLS to the absolute directory of pinned Qlty and Gitleaks binaries")
	}
	tools, err := os.OpenRoot(binaries)
	if err != nil {
		return "", err
	}
	defer func() { _ = tools.Close() }()
	// Keep the bundle in the disposable client directory, which job cleanup
	// already owns. Never mount the job root or any part of the original repo.
	path := filepath.Join(r.offlineConfig, "scanner")
	if err := os.Mkdir(path, 0700); err != nil {
		return "", err
	}
	bundle, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = bundle.Close() }()
	if err := copyQltyProbeTool(tools, bundle, "qlty", linuxQltyProbeBinary); err != nil {
		return "", err
	}
	if err := copyQltyProbeTool(tools, bundle, "gitleaks", gitleaksProbeBinary); err != nil {
		return "", err
	}
	if err := r.writeQltyProbeConfiguration(ctx, bundle); err != nil {
		return "", err
	}
	return path, nil
}

func (r *jobRunner) writeQltyProbeConfiguration(ctx context.Context, bundle *os.Root) error {
	workspace := filepath.Join(bundle.Name(), "workspace")
	if err := bundle.MkdirAll("workspace/.qlty", 0700); err != nil {
		return err
	}
	for name, content := range map[string]string{
		"gitleaks.toml": gitleaksProbeConfig, "gitleaks.ignore": "",
		"workspace/.qlty/qlty.toml": qltyProbeConfig, "workspace/trigger.txt": qltyProbeTrigger,
	} {
		if err := bundle.WriteFile(name, []byte(content), 0400); err != nil {
			return err
		}
	}
	if err := bundle.WriteFile("scan.sh", []byte(qltyProbeScript), 0500); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, args := range [][]string{
		{"init", "--quiet", "--template=", "-b", "probe"},
		{"add", "--", "trigger.txt"},
		{"-c", "user.name=Whirlwind", "-c", "user.email=whirlwind@example.invalid", "-c", "commit.gpgSign=false", "commit", "--quiet", "-m", "scanner trigger"},
	} {
		if _, err := r.git(ctx, workspace, args...); err != nil {
			return err
		}
	}
	return nil
}

func copyQltyProbeTool(source, destination *os.Root, name, expected string) error {
	const sizeLimit = 100 << 20
	info, err := source.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > sizeLimit {
		return fmt.Errorf("Qlty probe tool %s must be a regular pinned binary", name)
	}
	input, err := source.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := destination.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0500)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, sizeLimit+1))
	if err := errors.Join(err, output.Close()); err != nil {
		return err
	}
	if n > sizeLimit || hex.EncodeToString(hash.Sum(nil)) != expected {
		return fmt.Errorf("Qlty probe tool %s failed its pinned SHA-256 check", name)
	}
	return nil
}

func validateLinuxQltyProbeOutput(raw string) error {
	var sarif struct {
		Version string
		Runs    []struct {
			Tool struct {
				Driver struct{ Name, SemanticVersion string }
			}
			Invocations []struct {
				ExecutionSuccessful        bool
				ToolExecutionNotifications []struct {
					Descriptor struct{ ID string }
				}
			}
			Results []json.RawMessage
		}
	}
	if err := json.Unmarshal([]byte(raw), &sarif); err != nil || sarif.Version != "2.1.0" || len(sarif.Runs) != 1 {
		return errors.New("Qlty probe lacks a complete SARIF 2.1.0 run")
	}
	run := sarif.Runs[0]
	if run.Tool.Driver.Name != "qlty" || run.Tool.Driver.SemanticVersion != linuxQltyProbeVersion || run.Results == nil || len(run.Results) != 0 || len(run.Invocations) != 1 {
		return errors.New("Qlty probe lacks the pinned clean scanner result")
	}
	invocation := run.Invocations[0]
	if !invocation.ExecutionSuccessful || len(invocation.ToolExecutionNotifications) != 1 ||
		invocation.ToolExecutionNotifications[0].Descriptor.ID != probeDriverSuccess {
		return errors.New("Qlty probe lacks a successful scanner invocation")
	}
	return nil
}
