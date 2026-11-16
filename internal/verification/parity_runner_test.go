package verification

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	runnerPrintfBinary = "/usr/bin/printf"
)

func TestParityRunnerIndependentSubprocessPreservesCommandsAndReportsEveryOutcome(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native macOS runner acceptance requires macOS")
	}
	_, source, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	binary := filepath.Join(t.TempDir(), "whirlwind-verify")
	build := exec.Command("go", "build", "-o", binary, "./cmd/whirlwind-verify")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("independent verifier executable unavailable: %v\n%s", err, output)
	}
	project := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", project}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		raw, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	git("init", "-b", "fixture")
	requireVerificationSuccess(t, os.WriteFile(filepath.Join(project, "source.txt"), []byte("frozen candidate\n"), 0600))
	git("add", ".")
	git("commit", "-m", "candidate")
	head, tree := git("rev-parse", "HEAD"), git("rev-parse", "HEAD^{tree}")
	root := t.TempDir()
	requireVerificationSuccess(t, os.Chmod(root, 0700))
	manifest := map[string]any{"version": 1, "job_id": "verification-job", "plan_id": "plan", "plan_version": 2, "plan_hash": "approved-bytes", "configuration_hash": "approved-config", "candidate_commit": head, "candidate_tree": tree, "base_commit": head, "repository": project, "root": root, "launcher": filepath.Join(repoRoot, "bridges", "nono", "launch.mjs"), "commands": []map[string]any{
		{"name": "failure", "command": []string{"/bin/sh", "-c", "printf failure >&2; exit 7"}, "timeout_seconds": 5},
		{"name": "independent", "command": []string{runnerPrintfBinary, "%s\n", "literal;$(must-not-execute)"}, "timeout_seconds": 5},
		{"name": "blocked", "command": []string{"/usr/bin/touch", "source.txt"}, "depends_on": []string{"failure"}, "timeout_seconds": 5},
		{"name": "missing", "command": []string{"whirlwind-missing-tool"}, "timeout_seconds": 5},
		{"name": "timeout", "command": []string{"/bin/sleep", "3"}, "timeout_seconds": 1},
		{"name": "malformed-sarif", "command": []string{runnerPrintfBinary, "%s", "{}"}, "timeout_seconds": 5, "report_format": "sarif"},
		{"name": "valid-sarif", "command": []string{runnerPrintfBinary, "%s", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"approved-scanner"}},"invocations":[{"executionSuccessful":true}],"results":[]}]}`}, "timeout_seconds": 5, "report_format": "sarif"},
		{"name": "incomplete-tests", "command": []string{runnerPrintfBinary, "%s", `{"Action":"run","Package":"example","Test":"TestResult"}`}, "timeout_seconds": 5, "report_format": "go-test-json"},
		{"name": "malformed-diagnostic", "command": []string{runnerPrintfBinary, "%s", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"approved-scanner"}},"invocations":[{"executionSuccessful":true,"toolExecutionNotifications":[{"level":"note","message":null}]}],"results":[]}]}`}, "timeout_seconds": 5, "report_format": "sarif"},
	}}
	raw, _ := json.Marshal(manifest)
	command := exec.Command(binary)
	command.Stdin = bytes.NewReader(raw)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "DATABASE_URL=ambient-secret-must-not-arrive", "HOME=" + t.TempDir()}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("runner did not complete command report: %v\n%s", err, output)
	}
	reportBytes, err := os.ReadFile(filepath.Join(root, "report.json"))
	requireVerificationSuccess(t, err)
	assertIndependentRunnerOutcomes(t, reportBytes)
	if git("status", "--porcelain") != "" {
		t.Fatal("runner changed original repository")
	}
	// An identical retry reuses the command journal instead of rerunning effects.
	retry := exec.Command(binary)
	retry.Stdin = bytes.NewReader(raw)
	retry.Env = command.Env
	if output, err := retry.CombinedOutput(); err != nil {
		t.Fatalf("journal reconstruction failed: %v %s", err, output)
	}
	again, err := os.ReadFile(filepath.Join(root, "report.json"))
	if err != nil || !bytes.Equal(reportBytes, again) {
		t.Fatalf("completed journal replay changed report: %v", err)
	}
}

func assertIndependentRunnerOutcomes(t *testing.T, reportBytes []byte) {
	t.Helper()
	var report struct {
		Passed  bool `json:"passed"`
		Results []struct {
			Name       string
			Status     string
			StdoutPath string `json:"stdout_path"`
			StderrPath string `json:"stderr_path"`
			Command    []string
			ExitCode   *int `json:"exit_code"`
			Error      string
		}
	}
	requireVerificationSuccess(t, json.Unmarshal(reportBytes, &report))
	if report.Passed || len(report.Results) != 9 {
		t.Fatalf("incomplete failure report: %s", reportBytes)
	}
	want := []string{"fail", "pass", "error", "error", "timeout", "error", "pass", "error", "error"}
	for index, result := range report.Results {
		if result.Status != want[index] {
			t.Fatalf("command %s status %s want %s; report=%s", result.Name, result.Status, want[index], reportBytes)
		}
		if result.StdoutPath == "" || result.StderrPath == "" {
			t.Fatalf("missing durable streams: %+v", result)
		}
	}
	if code := report.Results[0].ExitCode; code == nil || *code != 7 {
		t.Fatalf("real failure exit code lost: %s", reportBytes)
	}
	diagnostic := report.Results[8]
	assertMalformedDiagnosticOutcome(t, diagnostic.ExitCode, diagnostic.Error, diagnostic.StdoutPath)
	stdout, err := os.ReadFile(report.Results[1].StdoutPath)
	if err != nil || string(stdout) != "literal;$(must-not-execute)\n" {
		t.Fatalf("argv rewritten/interpreted: %q %v", stdout, err)
	}
	stderr, err := os.ReadFile(report.Results[0].StderrPath)
	if err != nil || !strings.Contains(string(stderr), "failure") {
		t.Fatalf("stderr lost: %q %v", stderr, err)
	}
}

func assertMalformedDiagnosticOutcome(t *testing.T, exitCode *int, message, stdoutPath string) {
	t.Helper()
	if exitCode == nil || *exitCode != 0 || message == "" {
		t.Fatalf("malformed diagnostics need exit zero and an evidence error: exit=%v error=%q", exitCode, message)
	}
	rawDiagnostic, err := os.ReadFile(stdoutPath)
	if err != nil || !strings.Contains(string(rawDiagnostic), `"message":null`) {
		t.Fatalf("malformed diagnostic raw evidence was not retained: %q %v", rawDiagnostic, err)
	}
}
