package verification

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/process"
)

type capped struct {
	data  []byte
	total int
}

type checkOutput struct {
	stdout capped
	stderr capped
}

// Write retains the first MiB of check output while reporting all input bytes consumed to the subprocess.
func (b *capped) Write(p []byte) (int, error) {
	n := len(p)
	b.total += n
	if len(b.data) < 1024*1024 {
		remaining := min(len(p), 1024*1024-len(b.data))
		b.data = append(b.data, p[:remaining]...)
	}
	return n, nil
}

func (b *capped) String() string { return string(b.data) }

func (b *capped) Len() int { return len(b.data) }

func runCheckProfile(ctx context.Context, cwd string, c model.Check, profile execution.Profile) Result {
	r := Result{Name: c.Name, Command: c.Command}
	var profileErr error
	profile, profileErr = execution.ForCheck(profile, c.OutputPaths)
	if profileErr == nil {
		profile, profileErr = execution.ForCommand(profile, c.Command)
	}
	if profileErr == nil {
		r.ExecutionProfile = profile.Digest()
	}
	r.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	start := time.Now()
	timeout := time.Duration(c.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	r.TimeoutSeconds = int(timeout.Seconds())
	run, cancel := context.WithTimeout(ctx, timeout)
	output, err := executeCheck(run, cwd, c, profile, profileErr)
	cancel()
	r.Passed = err == nil
	r.Output = output.stdout.String()
	if isQltyCheck(c) {
		r.Diagnostics = output.stderr.String()
	}
	r.Truncated = checkOutputTruncated(output)
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	setCheckExitCode(&r, err)
	if err != nil {
		r.Error = err.Error()
	}
	r.DurationMS = time.Since(start).Milliseconds()
	if r.Passed && isQltySmellsCheck(c) {
		if err := qltySmellsError(r.Output); err != nil {
			r.Passed = false
			r.Error = err.Error()
		}
	}
	return r
}

func executeCheck(ctx context.Context, cwd string, check model.Check, profile execution.Profile, profileErr error) (checkOutput, error) {
	var output checkOutput
	if profileErr != nil {
		return output, profileErr
	}
	if len(check.Command) == 0 {
		return output, fmt.Errorf("missing command")
	}
	cmd, err := execution.Command(ctx, check.Command, profile)
	if err != nil {
		return output, err
	}
	cmd.Dir = cwd
	cmd.Stdout = &output.stdout
	if isQltyCheck(check) {
		cmd.Stderr = &output.stderr
	} else {
		cmd.Stderr = &output.stdout
	}
	return output, process.Run(cmd)
}

func isQltyCheck(check model.Check) bool {
	return len(check.Command) > 1 && filepath.Base(check.Command[0]) == "qlty" &&
		(check.Command[1] == "check" || check.Command[1] == "smells")
}

func checkOutputTruncated(output checkOutput) bool {
	return output.stdout.total > output.stdout.Len() || output.stderr.total > output.stderr.Len()
}

func setCheckExitCode(result *Result, err error) {
	if err == nil {
		code := 0
		result.ExitCode = &code
		return
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitCode()
		result.ExitCode = &code
	}
}
