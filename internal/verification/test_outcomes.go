package verification

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

type testOutcomeEvidence struct {
	failed, passed map[string]bool
}

// RegressionTestsPassed requires complete assertion reports for one check. Every
// newly failing red test must explicitly pass in green; existing failures remain
// advisory, and missing or skipped regression tests never establish a pass.
// Callers retain raw Results and separately bind their source snapshots.
func RegressionTestsPassed(base, red, green Result) bool {
	if !sameBaselineCheck(base, red) || !sameBaselineCheck(red, green) {
		return false
	}
	reports := make([]testOutcomeEvidence, 0, 3)
	for _, result := range []Result{base, red, green} {
		report, err := checkedTestOutcomes(result)
		if err != nil {
			return false
		}
		reports = append(reports, report)
	}
	return regressionOutcomesPassed(reports[0], reports[1], reports[2])
}

func regressionOutcomesPassed(base, red, green testOutcomeEvidence) bool {
	for id := range green.failed {
		if !base.failed[id] {
			return false
		}
	}
	newFailure := false
	for id := range red.failed {
		if base.failed[id] {
			continue
		}
		if !green.passed[id] {
			return false
		}
		newFailure = true
	}
	return newFailure
}

func checkedTestOutcomes(result Result) (testOutcomeEvidence, error) {
	if err := testOutcomeProcessEvidence(result); err != nil {
		return testOutcomeEvidence{}, err
	}
	findings, err := baselineFindings(result, false)
	if err != nil {
		return testOutcomeEvidence{}, err
	}
	if (*result.ExitCode == 0) != (len(findings) == 0) {
		return testOutcomeEvidence{}, errors.New("test outcome evidence contradicts process exit status")
	}
	passed, err := testPassedOutcomes(result.Output)
	report := testOutcomeEvidence{failed: map[string]bool{}, passed: passed}
	for _, finding := range findings {
		report.failed[finding.identity] = true
	}
	return report, err
}

func testOutcomeProcessEvidence(result Result) error {
	if err := baselineProcessEvidence(result); err != nil {
		return err
	}
	if baselineQlty(result) || result.Passed != (*result.ExitCode == 0) {
		return errors.New("test outcomes require raw structured assertion evidence")
	}
	return nil
}

func testPassedOutcomes(output string) (map[string]bool, error) {
	if strings.HasPrefix(strings.TrimSpace(output), "<") {
		var suite testOutcomeJUnitSuite
		if err := xml.Unmarshal([]byte(output), &suite); err != nil {
			return nil, err
		}
		passed := map[string]bool{}
		return passed, suite.collectPassed("", passed)
	}
	return goPassedOutcomes(output)
}

func goPassedOutcomes(output string) (map[string]bool, error) {
	passed := map[string]bool{}
	decoder := json.NewDecoder(strings.NewReader(output))
	for {
		var event baselineGoEvent
		err := decoder.Decode(&event)
		if err == io.EOF {
			return passed, nil
		}
		if err != nil {
			return nil, err
		}
		if err := recordGoTestOutcome(passed, event); err != nil {
			return nil, err
		}
	}
}

func recordGoTestOutcome(passed map[string]bool, event baselineGoEvent) error {
	if event.Test == "" {
		return nil
	}
	switch event.Action {
	case "pass", "fail", "skip":
		return recordTestOutcome(passed, event.Package+"/"+event.Test, event.Action == "pass")
	default:
		return nil
	}
}

func recordTestOutcome(passed map[string]bool, id string, success bool) error {
	if _, exists := passed[id]; exists {
		return errors.New("test outcome evidence has ambiguous duplicate test identity: " + id)
	}
	passed[id] = success
	return nil
}

type testOutcomeJUnitCase struct {
	Name     string     `xml:"name,attr"`
	Class    string     `xml:"classname,attr"`
	Status   string     `xml:"status,attr"`
	Failures []struct{} `xml:"failure"`
	Skipped  []struct{} `xml:"skipped"`
}

type testOutcomeJUnitSuite struct {
	Name   string                  `xml:"name,attr"`
	Suites []testOutcomeJUnitSuite `xml:"testsuite"`
	Cases  []testOutcomeJUnitCase  `xml:"testcase"`
}

func (suite testOutcomeJUnitSuite) collectPassed(prefix string, passed map[string]bool) error {
	prefix += suite.Name + "/"
	for _, test := range suite.Cases {
		id := prefix + test.Class + "/" + test.Name
		if err := recordTestOutcome(passed, id, test.passed()); err != nil {
			return err
		}
	}
	for _, child := range suite.Suites {
		if err := child.collectPassed(prefix, passed); err != nil {
			return err
		}
	}
	return nil
}

func (test testOutcomeJUnitCase) passed() bool {
	return len(test.Failures) == 0 && len(test.Skipped) == 0 && (test.Status == "" || test.Status == "run")
}
