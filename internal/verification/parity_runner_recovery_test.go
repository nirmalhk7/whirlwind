package verification

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	recoveryRevParseArg    = "rev-parse"
	recoveryTrueBinary     = "/usr/bin/true"
	recoveryReportFilename = "report.json"
	recoveryQltyConfigPath = ".qlty/qlty.toml"
)

func parityRunnerFixture(t *testing.T) (string, JobManifest) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("native verifier requires macOS")
	}
	_, source, _, _ := runtime.Caller(0)
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	binary := filepath.Join(t.TempDir(), "whirlwind-verify")
	build := exec.Command("go", "build", "-o", binary, "./cmd/whirlwind-verify")
	build.Dir = repository
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build runner: %v %s", err, raw)
	}
	original := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", original}, args...)...)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		raw, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("Git fixture: %v %s", err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	git("init", "-b", "fixture")
	git("commit", "--allow-empty", "-m", "candidate")
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return binary, JobManifest{Version: 1, JobID: "job", PlanID: "plan", PlanVersion: 1, PlanHash: "plan-bytes", ConfigurationHash: "config", CandidateCommit: git(recoveryRevParseArg, "HEAD"), CandidateTree: git(recoveryRevParseArg, "HEAD^{tree}"), BaseCommit: git(recoveryRevParseArg, "HEAD"), Repository: original, Root: root, Launcher: filepath.Join(repository, "bridges", "nono", "launch.mjs")}
}

func TestParityRunnerCrashDoesNotReplayUncertainCommand(t *testing.T) {
	binary, manifest := parityRunnerFixture(t)
	manifest.Commands = []JobCommand{{Name: "interrupted", Command: []string{"/bin/sh", "-c", "printf 'run\\n' >> logs/counter; sleep 60"}, TimeoutSeconds: 90, OutputPaths: []string{"logs"}}, {Name: "independent", Command: []string{recoveryTrueBinary}, TimeoutSeconds: 5}}
	raw, _ := json.Marshal(manifest)
	command := exec.Command(binary)
	command.Stdin = bytes.NewReader(raw)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	var diagnostic bytes.Buffer
	command.Stderr = &diagnostic
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	counter := filepath.Join(manifest.Root, "checkout", "logs", "counter")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if raw, err := os.ReadFile(counter); err == nil && string(raw) == "run\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("command never reached uncertain execution: %s", diagnostic.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	retry := exec.Command(binary)
	retry.Stdin = bytes.NewReader(raw)
	retry.Env = command.Env
	if output, err := retry.CombinedOutput(); err != nil {
		t.Fatalf("interrupted journal recovery: %v %s", err, output)
	}
	body, err := os.ReadFile(filepath.Join(manifest.Root, recoveryReportFilename))
	if err != nil {
		t.Fatal(err)
	}
	var report JobReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Passed || len(report.Results) != 2 || report.Results[0].Status != "error" || !strings.Contains(report.Results[0].Error, "not replayed") || report.Results[1].Status != "pass" {
		t.Fatalf("uncertain command was replayed or lost: %s", body)
	}
	count, err := os.ReadFile(counter)
	if err != nil || string(count) != "run\n" {
		t.Fatalf("uncertain command repeated: %q %v", count, err)
	}
}

func TestParityRunnerRejectsChangedManifestAndDependencyOrder(t *testing.T) {
	binary, manifest := parityRunnerFixture(t)
	manifest.Commands = []JobCommand{{Name: "first", Command: []string{recoveryTrueBinary}, TimeoutSeconds: 5, DependsOn: []string{"later"}}, {Name: "later", Command: []string{recoveryTrueBinary}, TimeoutSeconds: 5}}
	invoke := func() ([]byte, error) {
		raw, _ := json.Marshal(manifest)
		command := exec.Command(binary)
		command.Stdin = bytes.NewReader(raw)
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
		return command.CombinedOutput()
	}
	if raw, err := invoke(); err == nil || !strings.Contains(string(raw), "must precede") {
		t.Fatalf("invalid dependency order accepted: %v %s", err, raw)
	}
	manifest.Commands[0].DependsOn = nil
	if raw, err := invoke(); err != nil {
		t.Fatalf("valid job rejected: %v %s", err, raw)
	}
	manifest.PlanVersion++
	if raw, err := invoke(); err == nil || !strings.Contains(string(raw), "another manifest") {
		t.Fatalf("cross-version journal reused: %v %s", err, raw)
	}
}

func TestParityRunnerEnvironmentCannotReplaceSandboxBootstrap(t *testing.T) {
	_, manifest := parityRunnerFixture(t)
	for _, key := range []string{"PATH", "HOME", "LD_PRELOAD", "NODE_OPTIONS", "WHIRLWIND_NONO_CLI", "NONO_ALLOW", "WHIRLWIND_PROCESS_REGISTRY", "XDG_CONFIG_HOME"} {
		t.Run(key, func(t *testing.T) {
			manifest.Commands = []JobCommand{{Name: "protected-environment", Command: []string{recoveryTrueBinary}, TimeoutSeconds: 5, Environment: map[string]string{key: "untrusted-command-value"}}}
			if err := manifest.Validate(); err == nil {
				t.Fatalf("approved command environment can replace protected bootstrap %s", key)
			}
		})
	}
	manifest.Commands = []JobCommand{{Name: "approved-environment", Command: []string{recoveryTrueBinary}, TimeoutSeconds: 5, Environment: map[string]string{"GOFLAGS": "-mod=readonly"}}}
	if err := manifest.Validate(); err != nil {
		t.Fatal("ordinary command environment rejected", err)
	}
}

func TestParityRunnerRetainsDeclaredArtifactsAndRejectsTampering(t *testing.T) {
	binary, manifest := parityRunnerFixture(t)
	manifest.Commands = []JobCommand{{Name: "report", Command: []string{"/bin/sh", "-c", "printf report > reports/result.txt"}, TimeoutSeconds: 5, OutputPaths: []string{"reports"}}}
	report, err := RunLocalJob(context.Background(), binary, manifest, nil)
	if err != nil || !report.Passed {
		t.Fatalf("artifact command failed: %+v %v", report, err)
	}
	raw, err := os.ReadFile(filepath.Join(manifest.Root, recoveryReportFilename))
	requireVerificationSuccess(t, err)
	var evidence struct {
		Results []struct {
			Artifacts []struct {
				Path, Kind, SHA256, Location string
				Bytes                        int64
			}
		}
	}
	requireVerificationSuccess(t, json.Unmarshal(raw, &evidence))
	var retained string
	for _, artifact := range evidence.Results[0].Artifacts {
		if artifact.Path == "reports/result.txt" {
			if artifact.Kind != "file" || artifact.Bytes != 6 || artifact.SHA256 != "845e91831319e89c4d656bdb80c278ac09a7230d61e5dfd2e1b1fbb436ac8917" {
				t.Fatalf("artifact identity missing: %+v", artifact)
			}
			retained = filepath.Join(manifest.Root, artifact.Location)
		}
	}
	if retained == "" {
		t.Fatal("declared command artifact not retained in durable report")
	}
	content, err := os.ReadFile(retained)
	if err != nil || string(content) != "report" {
		t.Fatalf("wrong retained artifact: %q %v", content, err)
	}
	requireVerificationSuccess(t, os.WriteFile(retained, []byte("altered"), 0600))
	if _, err := RunLocalJob(context.Background(), binary, manifest, nil); err == nil {
		t.Fatal("tampered retained artifact accepted during journal recovery")
	}
	request, _ := json.Marshal(manifest)
	retry := exec.Command(binary)
	retry.Stdin = bytes.NewReader(request)
	retry.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	if output, err := retry.CombinedOutput(); err == nil || !strings.Contains(string(output), "artifact") {
		t.Fatalf("runner itself certified altered retained artifact: %v %s", err, output)
	}
}

func TestParityRunnerRejectsArtifactSymlinks(t *testing.T) {
	binary, manifest := parityRunnerFixture(t)
	manifest.Commands = []JobCommand{{Name: "linked", Command: []string{"/bin/ln", "-s", "/etc/hosts", "reports/link"}, TimeoutSeconds: 5, OutputPaths: []string{"reports"}}}
	report, err := RunLocalJob(context.Background(), binary, manifest, nil)
	if err != nil {
		t.Fatalf("artifact error must retain complete command result: %v", err)
	}
	if report.Passed || len(report.Results) != 1 || report.Results[0].Status != "error" || !strings.Contains(report.Results[0].Error, "artifact") {
		t.Fatalf("unsafe output artifact accepted: %+v", report)
	}
}

func TestParityRunnerCopiesFrozenExternalQualityConfiguration(t *testing.T) {
	binary, manifest := parityRunnerFixture(t)
	qualityRoot := manifest.Root
	manifest.Root = filepath.Join(qualityRoot, "jobs", manifest.JobID)
	values := map[string]any{}
	raw, _ := json.Marshal(manifest)
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	values["quality"] = map[string]any{"root": qualityRoot, "files": map[string]string{recoveryQltyConfigPath: "config_version = \"0\"\n"}}
	values["commands"] = []map[string]any{{"name": "inspect-config", "command": []string{"/bin/cat", recoveryQltyConfigPath}, "timeout_seconds": 5}}
	raw, _ = json.Marshal(values)
	command := exec.Command(binary)
	command.Stdin = bytes.NewReader(raw)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("external configuration runner failed: %v %s", err, output)
	}
	body, err := os.ReadFile(filepath.Join(manifest.Root, recoveryReportFilename))
	if err != nil {
		t.Fatal(err)
	}
	var report JobReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Passed || len(report.Results) != 1 || report.Results[0].Output != "config_version = \"0\"\n" {
		t.Fatalf("approved external configuration was not copied into staging: %+v", report)
	}
	info, err := os.Lstat(filepath.Join(manifest.Root, "checkout", ".qlty", "qlty.toml"))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("staged configuration must be a real file")
	}
	if _, err := os.Stat(filepath.Join(manifest.Repository, ".qlty")); !os.IsNotExist(err) {
		t.Fatal("runner wrote external Qlty config into original repository")
	}
}

func TestParityRunnerRejectsQualityConfigurationSourceOverrides(t *testing.T) {
	_, manifest := parityRunnerFixture(t)
	qualityRoot := manifest.Root
	manifest.Root = filepath.Join(qualityRoot, "jobs", manifest.JobID)
	manifest.Commands = []JobCommand{{Name: "test", Command: []string{recoveryTrueBinary}, TimeoutSeconds: 5}}
	for _, test := range []struct{ name, path, root string }{
		{name: "source code", path: "source.go", root: qualityRoot},
		{name: "traversal", path: "../outside", root: qualityRoot},
		{name: "Git metadata", path: ".git/config", root: qualityRoot},
		{name: "different external root", path: recoveryQltyConfigPath, root: filepath.Join(qualityRoot, "other")},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal(manifest)
			var values map[string]any
			if err := json.Unmarshal(raw, &values); err != nil {
				t.Fatal(err)
			}
			values["quality"] = map[string]any{"root": test.root, "files": map[string]string{test.path: "configuration"}}
			raw, _ = json.Marshal(values)
			var request JobManifest
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			if err := request.Validate(); err == nil {
				t.Fatalf("unsafe external quality manifest accepted: %s", test.name)
			}
		})
	}
}
