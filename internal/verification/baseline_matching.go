package verification

import "errors"

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
