package verification

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type baselineFinding struct {
	identity string
	severity int
	metrics  map[string]float64
}

// CompareBaseline gates head on new or worsened findings. Original process
// evidence is retained; accepted debt has explicit baseline comparison metadata.
// Only complete SARIF, Go test JSON, and JUnit failures can be grandfathered.
func CompareBaseline(base, head Report) Report {
	out := Report{Passed: true, Results: append([]Result(nil), head.Results...)}
	used := make([]bool, len(base.Results))
	for i := range out.Results {
		result := &out.Results[i]
		var previous *Result
		for n := range base.Results {
			if !used[n] && sameBaselineCheck(base.Results[n], *result) {
				used[n] = true
				previous = &base.Results[n]
				break
			}
		}
		if err := compareBaselineResult(previous, result); err != nil {
			result.Passed = false
			result.Error = joinBaselineError(result.Error, err.Error())
		}
		if !result.Passed {
			out.Passed = false
		}
	}
	for i, r := range base.Results {
		if !used[i] {
			r.Passed = false
			r.Error = joinBaselineError(r.Error, "baseline comparison blocked: configured baseline check missing from head")
			out.Results = append(out.Results, r)
			out.Passed = false
		}
	}
	return out
}

func sameBaselineCheck(a, b Result) bool {
	if a.Name != b.Name || len(a.Command) != len(b.Command) {
		return false
	}
	for n := range a.Command {
		if a.Command[n] != b.Command[n] {
			return false
		}
	}
	return true
}

func compareBaselineResult(base *Result, head *Result) error {
	if err := baselineProcessEvidence(*head); err != nil {
		return err
	}
	quality := baselineQlty(*head)
	if head.Passed && !quality {
		return nil
	}
	findings, err := baselineFindings(*head, quality)
	if err != nil {
		return err
	}
	return compareBaselineFindings(base, head, quality, findings)
}

func compareBaselineFindings(base *Result, head *Result, quality bool, findings []baselineFinding) error {
	if len(findings) == 0 {
		return validateEmptyBaselineFindings(*head)
	}
	head.Passed = false
	previous, err := previousBaselineFindings(base, quality)
	if err != nil {
		return err
	}
	if err := matchBaselineFindings(previous, findings); err != nil {
		return err
	}
	head.Passed = true
	metadata, _ := json.Marshal(map[string]any{"baseline_comparison": map[string]any{"status": "preexisting_advisory", "preexisting_findings": len(findings), "baseline_findings": len(previous), "raw_exit_code": *head.ExitCode}})
	head.Output += "\n\n" + string(metadata)
	head.Error = joinBaselineError(head.Error, "Baseline comparison accepted preexisting findings as advisory; raw process evidence retained")
	return nil
}

func validateEmptyBaselineFindings(head Result) error {
	if *head.ExitCode != 0 || !head.Passed {
		return errors.New("baseline comparison blocked: failed process has no identifiable findings")
	}
	return nil
}

func previousBaselineFindings(base *Result, quality bool) ([]baselineFinding, error) {
	if base == nil {
		return nil, errors.New("baseline comparison blocked: no matching baseline check")
	}
	if err := baselineProcessEvidence(*base); err != nil {
		return nil, fmt.Errorf("unusable baseline: %w", err)
	}
	previous, err := baselineFindings(*base, quality)
	if err != nil {
		return nil, fmt.Errorf("unusable baseline: %w", err)
	}
	return previous, nil
}

type baselineMatcher struct {
	previous, findings []baselineFinding
	assignment         []int
}

func matchBaselineFindings(previous, findings []baselineFinding) error {
	matcher := baselineMatcher{previous: previous, findings: findings, assignment: make([]int, len(previous))}
	for i := range matcher.assignment {
		matcher.assignment[i] = -1
	}
	for index, finding := range findings {
		if !matcher.match(index, make([]bool, len(previous))) {
			return errors.New("baseline comparison blocked: new or worsened finding: " + finding.identity)
		}
	}
	return nil
}

func (matcher *baselineMatcher) match(index int, seen []bool) bool {
	for n, old := range matcher.previous {
		if seen[n] || baselineWorsened(old, matcher.findings[index]) {
			continue
		}
		seen[n] = true
		if matcher.assignment[n] == -1 || matcher.match(matcher.assignment[n], seen) {
			matcher.assignment[n] = index
			return true
		}
	}
	return false
}

func joinBaselineError(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func baselineFindings(r Result, quality bool) ([]baselineFinding, error) {
	if quality {
		return baselineSARIF(r.Output)
	}
	if strings.HasPrefix(strings.TrimSpace(r.Output), "<") {
		return baselineJUnit(r.Output)
	}
	return baselineGoTests(r.Output)
}

func baselineWorsened(base, head baselineFinding) bool {
	if base.identity != head.identity || head.severity > base.severity {
		return true
	}
	for key, value := range head.metrics {
		old, ok := base.metrics[key]
		if !ok || value > old {
			return true
		}
	}
	return false
}

// IsStructuredTestFailure accepts only completed named assertion failures.
func IsStructuredTestFailure(result Result) bool {
	if result.Passed || baselineProcessEvidence(result) != nil || baselineQlty(result) {
		return false
	}
	findings, err := baselineFindings(result, false)
	return err == nil && len(findings) > 0
}
