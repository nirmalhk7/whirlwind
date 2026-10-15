package verification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/process"
)

type Result struct {
	Command        []string `json:"command"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	StartedAt      string   `json:"started_at"`
	FinishedAt     string   `json:"finished_at"`
	ExitCode       *int     `json:"exit_code"`
	Truncated      bool     `json:"truncated"`
	OutputArtifact string   `json:"output_artifact,omitempty"`
	Name           string   `json:"name"`
	Passed         bool     `json:"passed"`
	Output         string   `json:"output"`
	Error          string   `json:"error,omitempty"`
	DurationMS     int64    `json:"duration_ms"`
}
type Report struct {
	Passed  bool     `json:"passed"`
	Results []Result `json:"results"`
}
type capped struct {
	bytes.Buffer
	total int
}

// Write retains the first MiB of check output while reporting all input bytes consumed to the subprocess.
func (b *capped) Write(p []byte) (int, error) {
	n := len(p)
	b.total += n
	if b.Len() < 1024*1024 {
		remaining := min(len(p), 1024*1024-b.Len())
		_, _ = b.Buffer.Write(p[:remaining])
	}
	return n, nil
}

// Run executes checks sequentially with per-check timeouts, captures bounded diagnostics, and marks the report failed on any error.
func Run(ctx context.Context, cwd string, checks []model.Check) Report {
	report, _ := RunObserved(ctx, cwd, checks, nil)
	return report
}

func RunObserved(ctx context.Context, cwd string, checks []model.Check, observe func(Result) error) (Report, error) {
	out := Report{Passed: len(checks) > 0, Results: []Result{}}
	for _, c := range checks {
		r := runCheck(ctx, cwd, c)
		if !r.Passed {
			out.Passed = false
		}
		out.Results = append(out.Results, r)
		if observe != nil {
			if err := observe(r); err != nil {
				out.Passed = false
				return out, err
			}
		}
		if ctx.Err() != nil {
			out.Passed = false
			return out, ctx.Err()
		}
	}
	return out, nil
}

// runCheck captures one bounded check result before observers or cancellation are considered.
func runCheck(ctx context.Context, cwd string, c model.Check) Result {
	r := Result{Name: c.Name, Command: c.Command}
	r.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	start := time.Now()
	timeout := time.Duration(c.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	r.TimeoutSeconds = int(timeout.Seconds())
	run, cancel := context.WithTimeout(ctx, timeout)
	var b capped
	var e error
	if len(c.Command) == 0 {
		e = fmt.Errorf("missing command")
	} else {
		cmd := process.Command(run, c.Command[0], c.Command[1:]...)
		cmd.Dir = cwd
		cmd.Stdout = &b
		cmd.Stderr = &b
		e = process.Run(cmd)
	}
	cancel()
	r.Passed = e == nil
	r.Output = b.String()
	r.Truncated = b.total > b.Len()
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if e == nil {
		code := 0
		r.ExitCode = &code
	} else {
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			code := exit.ExitCode()
			r.ExitCode = &code
		}
	}
	if e != nil {
		r.Error = e.Error()
	}
	r.DurationMS = time.Since(start).Milliseconds()
	return r
}

// Select keeps baseline checks, adds checks for matching path, code-type, or API rules, and selects the highest matched risk.
func Select(p model.Policy, files []string, diff ...string) (string, []model.Check) {
	symbols := changedAPISymbols(diff)
	extensions := make([]string, len(files))
	for n, file := range files {
		extensions[n] = filepath.Ext(file)
	}
	levels := map[string]int{"low": 0, "medium": 1, "high": 2, "critical": 3}
	risk := "low"
	matchedAny := false
	checks := append([]model.Check{}, p.Checks...)
	seen := map[string]bool{}
	for _, c := range checks {
		seen[c.Name] = true
	}
	for _, rule := range p.RiskRules {
		if !matchesAny(rule.Paths, files, policy.Match) &&
			!matchesAny(rule.APISymbols, symbols, policy.Match) &&
			!matchesAny(rule.CodeTypes, extensions, strings.EqualFold) {
			continue
		}
		matchedAny = true
		if levels[rule.Level] > levels[risk] {
			risk = rule.Level
		}
		for _, c := range rule.Checks {
			if !seen[c.Name] {
				checks = append(checks, c)
				seen[c.Name] = true
			}
		}
	}
	if !matchedAny {
		risk = "medium"
	}
	return risk, checks
}

// matchesAny reports whether any configured selector matches any changed value.
func matchesAny(patterns, values []string, match func(string, string) bool) bool {
	for _, pattern := range patterns {
		for _, value := range values {
			if match(pattern, value) {
				return true
			}
		}
	}
	return false
}

var apiPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^[-+]\s*func\s+(?:\([^)]*\)\s*)?([A-Z][A-Za-z0-9_]*)`),
	regexp.MustCompile(`(?m)^[-+]\s*type\s+([A-Z][A-Za-z0-9_]*)`),
	regexp.MustCompile(`(?m)^[-+]\s*export\s+(?:default\s+)?(?:function|class|type|interface|const)\s+([A-Za-z_][A-Za-z0-9_]*)`),
	regexp.MustCompile(`(?m)^[-+].*HandleFunc\("([A-Z]+ /[^" ]*)"`),
}

func changedAPISymbols(diff []string) []string {
	symbols := []string{}
	for _, patch := range diff {
		for _, pattern := range apiPatterns {
			for _, match := range pattern.FindAllStringSubmatch(patch, -1) {
				symbols = append(symbols, match[1])
			}
		}
	}
	return symbols
}
