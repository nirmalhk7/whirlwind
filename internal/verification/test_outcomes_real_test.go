package verification

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegressionEvidenceFromActualGoTestJSON(t *testing.T) {
	repo := t.TempDir()
	write := func(file, text string) {
		t.Helper()
		requireVerificationSuccess(t, os.WriteFile(filepath.Join(repo, file), []byte(text), 0600))
	}
	write("go.mod", "module example.test/regression\ngo 1.23.0\n")
	write("result.go", "package result\nfunc Result() bool { return false }\n")
	run := func(filter ...string) Result {
		t.Helper()
		args := append([]string{"test", "-json", "-count=1"}, filter...)
		args = append(args, "./...")
		cmd := exec.CommandContext(t.Context(), "go", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GOPROXY=off", "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly")
		var diagnostic bytes.Buffer
		cmd.Stderr = &diagnostic
		output, err := cmd.Output()
		exit := 0
		errorText := ""
		if err != nil {
			var failed *exec.ExitError
			if !errors.As(err, &failed) || failed.ExitCode() != 1 {
				t.Fatalf("test command failed to run: %v %s", err, diagnostic.String())
			}
			exit, errorText = failed.ExitCode(), err.Error()
		}
		return Result{Name: "test", Command: append([]string{"go"}, args...), ExitCode: &exit, Passed: exit == 0, Error: errorText, Output: string(output)}
	}
	base := run()
	write("result_test.go", "package result\nimport \"testing\"\nfunc TestResult(t *testing.T) { if !Result() { t.Fatal(\"Result must return true\") } }\n")
	red := run()
	write("result.go", "package result\nfunc Result() bool { return true }\n")
	green := run()
	if !base.Passed || red.Passed || !green.Passed {
		t.Fatalf("fixture did not produce skipped baseline, red and green outcomes: %v/%v/%v", base.Passed, red.Passed, green.Passed)
	}
	if err := RegressionEvidenceError(base, red, green); err != nil {
		t.Fatalf("valid no-tests baseline and paired real regression did not qualify: %v", err)
	}
	omitted := run("-run", "^NoSuchTest$")
	if !omitted.Passed {
		t.Fatal("fixture was expected to exit zero without running the regression")
	}
	switch {
	case RegressionEvidenceError(base, red, omitted) == nil:
		t.Fatal("accepted a different command invocation as green")
	default:
		// Feed the real incomplete package output under the same command
		// identity to exercise the named regression gate independently.
		omitted.Command = green.Command
		if err := RegressionEvidenceError(base, red, omitted); err == nil || !strings.Contains(err.Error(), "example.test/regression/TestResult") {
			t.Fatalf("check accepted missing named regression or did not explain it: %v", err)
		}
	}
}
