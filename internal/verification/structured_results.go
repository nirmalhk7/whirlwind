package verification

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

type baselineGoEvent struct{ Action, Package, Test string }
type baselineGoReport struct {
	pending, terminal, failedPackages, failedTests map[string]bool
	findings                                       []baselineFinding
}

func baselineGoTests(output string) ([]baselineFinding, error) {
	decoder := json.NewDecoder(strings.NewReader(output))
	report := baselineGoReport{pending: map[string]bool{}, terminal: map[string]bool{}, failedPackages: map[string]bool{}, failedTests: map[string]bool{}, findings: []baselineFinding{}}
	for {
		var event baselineGoEvent
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil || event.Package == "" || event.Action == "" {
			return nil, errors.New("baseline comparison blocked: opaque or malformed test output")
		}
		if err := report.record(event); err != nil {
			return nil, err
		}
	}
	if err := report.validate(); err != nil {
		return nil, err
	}
	return report.findings, nil
}

func (report *baselineGoReport) record(event baselineGoEvent) error {
	if _, ok := report.terminal[event.Package]; !ok {
		report.terminal[event.Package] = false
	}
	switch event.Action {
	case "run":
		report.pending[event.Package+"/"+event.Test] = true
	case "fail", "pass", "skip":
		report.complete(event)
	case "start", "pause", "cont", "output", "bench":
	default:
		return errors.New("baseline comparison blocked: unsupported or failed test event")
	}
	return nil
}

func (report *baselineGoReport) complete(event baselineGoEvent) {
	if event.Test == "" {
		report.terminal[event.Package] = true
		if event.Action == "fail" {
			report.failedPackages[event.Package] = true
		}
		return
	}
	id := event.Package + "/" + event.Test
	delete(report.pending, id)
	if event.Action == "fail" {
		report.failedTests[event.Package] = true
		report.findings = append(report.findings, baselineFinding{identity: id, severity: 1})
	}
}

func (report baselineGoReport) validate() error {
	if len(report.pending) > 0 || len(report.terminal) == 0 {
		return errors.New("baseline comparison blocked: incomplete Go test report")
	}
	if err := report.validatePackages(); err != nil {
		return err
	}
	for pkg := range report.failedTests {
		if !report.failedPackages[pkg] {
			return errors.New("baseline comparison blocked: test failure lacks final package outcome")
		}
	}
	return nil
}

func (report baselineGoReport) validatePackages() error {
	for pkg, done := range report.terminal {
		if !done || (report.failedPackages[pkg] && !report.failedTests[pkg]) {
			return errors.New("baseline comparison blocked: incomplete tests or package infrastructure failure")
		}
	}
	return nil
}
