package verification

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"khan/internal/model"
	"khan/internal/policy"
	"khan/internal/process"
)

type Result struct {
	Name       string `json:"name"`
	Passed     bool   `json:"passed"`
	Output     string `json:"output"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}
type Report struct {
	Passed  bool     `json:"passed"`
	Results []Result `json:"results"`
}
type capped struct{ bytes.Buffer }

// Write retains the first MiB of check output while reporting all input bytes consumed to the subprocess.
func (b *capped) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 1024*1024 {
		remaining := min(len(p), 1024*1024-b.Len())
		_, _ = b.Buffer.Write(p[:remaining])
	}
	return n, nil
}

// Run executes checks sequentially with per-check timeouts, captures bounded diagnostics, and marks the report failed on any error.
func Run(ctx context.Context, cwd string, checks []model.Check) Report {
	out := Report{Passed: len(checks) > 0, Results: []Result{}}
	for _, c := range checks {
		r := Result{Name: c.Name}
		start := time.Now()
		timeout := time.Duration(c.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 10 * time.Minute
		}
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
		if e != nil {
			r.Error = e.Error()
			out.Passed = false
		}
		r.DurationMS = time.Since(start).Milliseconds()
		out.Results = append(out.Results, r)
		if ctx.Err() != nil {
			break
		}
	}
	return out
}

// Select keeps baseline checks, adds checks for matching path, code-type, or API rules, and selects the highest matched risk.
func Select(p model.Policy, files []string, diff ...string) (string, []model.Check) {
	symbols := []string{}
	for _, patch := range diff {
		for _, pattern := range apiPatterns {
			for _, match := range pattern.FindAllStringSubmatch(patch, -1) {
				symbols = append(symbols, match[1])
			}
		}
	}
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
