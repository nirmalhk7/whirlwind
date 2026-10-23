package verification

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type regressionTestOutcome struct{ name, action string }

func outcomeResult(format string, tests ...regressionTestOutcome) Result {
	output, exit := outcomeGoReport(tests)
	if format == "junit" {
		output, exit = outcomeJUnitReport(tests)
	}
	return regressionResult("tests", output, exit)
}

func outcomeGoReport(tests []regressionTestOutcome) (string, int) {
	events := []string{}
	exit, final := 0, "pass"
	for _, test := range tests {
		for _, action := range []string{"run", test.action} {
			encoded, _ := json.Marshal(baselineGoEvent{Action: action, Package: "example", Test: test.name})
			events = append(events, string(encoded))
		}
		if test.action == "fail" {
			exit, final = 1, "fail"
		}
	}
	encoded, _ := json.Marshal(baselineGoEvent{Action: final, Package: "example"})
	return strings.Join(append(events, string(encoded)), "\n"), exit
}

func outcomeJUnitReport(tests []regressionTestOutcome) (string, int) {
	cases, failures := "", 0
	for _, test := range tests {
		content := ""
		switch test.action {
		case "fail":
			content = `<failure message="assertion failed"/>`
			failures++
		case "skip":
			content = `<skipped/>`
		}
		cases += fmt.Sprintf(`<testcase classname="example" name="%s">%s</testcase>`, test.name, content)
	}
	exit := 0
	if failures > 0 {
		exit = 1
	}
	return fmt.Sprintf(`<testsuites><testsuite name="suite" tests="%d" failures="%d">%s</testsuite></testsuites>`, len(tests), failures, cases), exit
}

func TestRegressionTestsPassedPreservesExistingDebt(t *testing.T) {
	for _, format := range []string{"go", "junit"} {
		t.Run(format, func(t *testing.T) {
			base := outcomeResult(format, regressionTestOutcome{"TestOld", "fail"})
			red := outcomeResult(format, regressionTestOutcome{"TestOld", "fail"}, regressionTestOutcome{"TestRegression", "fail"})
			green := outcomeResult(format, regressionTestOutcome{"TestOld", "fail"}, regressionTestOutcome{"TestRegression", "pass"})
			if !RegressionTestsPassed(base, red, green) {
				t.Fatal("explicit regression pass blocked by existing assertion debt")
			}
			if base.Passed || red.Passed || green.Passed || *green.ExitCode != 1 {
				t.Fatal("comparison changed raw process evidence")
			}
		})
	}
}

func TestRegressionTestsPassedWithCleanBaseline(t *testing.T) {
	for _, format := range []string{"go", "junit"} {
		base := outcomeResult(format, regressionTestOutcome{"TestOld", "pass"})
		red := outcomeResult(format, regressionTestOutcome{"TestOld", "pass"}, regressionTestOutcome{"TestRegression", "fail"})
		green := outcomeResult(format, regressionTestOutcome{"TestOld", "pass"}, regressionTestOutcome{"TestRegression", "pass"})
		if !RegressionTestsPassed(base, red, green) {
			t.Fatalf("%s: complete regression red/green rejected", format)
		}
	}
}

func TestRegressionTestsPassedRequiresEveryNewFailureToPass(t *testing.T) {
	for _, format := range []string{"go", "junit"} {
		for _, action := range []string{"skip", "fail", "missing", "duplicate"} {
			t.Run(format+"/"+action, func(t *testing.T) {
				base := outcomeResult(format, regressionTestOutcome{"TestOld", "fail"})
				red := outcomeResult(format, regressionTestOutcome{"TestOld", "fail"}, regressionTestOutcome{"TestFirst", "fail"}, regressionTestOutcome{"TestSecond", "fail"})
				tests := []regressionTestOutcome{{"TestOld", "fail"}, {"TestFirst", "pass"}}
				if action != "missing" {
					tests = append(tests, regressionTestOutcome{"TestSecond", action})
				}
				if action == "duplicate" {
					tests = append(tests, regressionTestOutcome{"TestSecond", "pass"})
				}
				if RegressionTestsPassed(base, red, outcomeResult(format, tests...)) {
					t.Fatal("missing, skipped, failing, or ambiguous regression accepted")
				}
			})
		}
	}
}

func TestRegressionTestsPassedRequiresNewRedFailure(t *testing.T) {
	for _, format := range []string{"go", "junit"} {
		base := outcomeResult(format, regressionTestOutcome{"TestOld", "fail"})
		red := outcomeResult(format, regressionTestOutcome{"TestOld", "fail"})
		green := outcomeResult(format, regressionTestOutcome{"TestOld", "pass"})
		if RegressionTestsPassed(base, red, green) {
			t.Fatalf("%s: unrelated cleanup counted as new regression evidence", format)
		}
	}
}

func TestRegressionTestsPassedRejectsNewGreenFailure(t *testing.T) {
	for _, format := range []string{"go", "junit"} {
		base := outcomeResult(format, regressionTestOutcome{"TestOld", "pass"})
		red := outcomeResult(format, regressionTestOutcome{"TestOld", "pass"}, regressionTestOutcome{"TestRegression", "fail"})
		green := outcomeResult(format, regressionTestOutcome{"TestOld", "fail"}, regressionTestOutcome{"TestRegression", "pass"})
		if RegressionTestsPassed(base, red, green) {
			t.Fatalf("%s: new green failure counted as regression green", format)
		}
	}
}

func TestRegressionTestsPassedRejectsUnusableEvidence(t *testing.T) {
	for _, format := range []string{"go", "junit"} {
		t.Run(format, func(t *testing.T) { assertUnusableOutcomeEvidence(t, format) })
	}
}

func assertUnusableOutcomeEvidence(t *testing.T, format string) {
	t.Helper()
	for _, phase := range []int{0, 1, 2} {
		for _, defect := range []string{"opaque", "truncated", "infrastructure", "missing-exit", "incomplete", "different-command", "contradictory-exit"} {
			t.Run(fmt.Sprintf("phase-%d/%s", phase, defect), func(t *testing.T) {
				results := []Result{
					outcomeResult(format, regressionTestOutcome{"TestOld", "fail"}),
					outcomeResult(format, regressionTestOutcome{"TestOld", "fail"}, regressionTestOutcome{"TestRegression", "fail"}),
					outcomeResult(format, regressionTestOutcome{"TestOld", "fail"}, regressionTestOutcome{"TestRegression", "pass"}),
				}
				invalidateOutcomeEvidence(&results[phase], defect)
				if RegressionTestsPassed(results[0], results[1], results[2]) {
					t.Fatal("unusable structured report accepted")
				}
			})
		}
	}
}

func TestRegressionTestsPassedRejectsJUnitInfrastructureAndInactiveTests(t *testing.T) {
	base := outcomeResult("junit", regressionTestOutcome{"TestOld", "fail"})
	red := outcomeResult("junit", regressionTestOutcome{"TestOld", "fail"}, regressionTestOutcome{"TestRegression", "fail"})
	for _, content := range []string{`<error message="runner died"/>`, `<skipped/>`} {
		green := outcomeResult("junit", regressionTestOutcome{"TestOld", "fail"}, regressionTestOutcome{"TestRegression", "pass"})
		green.Output = strings.Replace(green.Output, `name="TestRegression">`, `name="TestRegression">`+content, 1)
		if RegressionTestsPassed(base, red, green) {
			t.Fatalf("nonpassing JUnit regression accepted: %s", content)
		}
	}
	green := outcomeResult("junit", regressionTestOutcome{"TestOld", "fail"}, regressionTestOutcome{"TestRegression", "pass"})
	green.Output = strings.Replace(green.Output, `name="TestRegression"`, `name="TestRegression" status="notrun"`, 1)
	if RegressionTestsPassed(base, red, green) {
		t.Fatal("JUnit inactive test accepted as explicit pass")
	}
}

func invalidateOutcomeEvidence(result *Result, defect string) {
	switch defect {
	case "opaque":
		result.Output = "FAIL"
	case "truncated":
		result.Truncated = true
	case "infrastructure":
		result.Error = "context deadline exceeded"
	case "missing-exit":
		result.ExitCode = nil
	case "incomplete":
		lines := strings.Split(result.Output, "\n")
		result.Output = strings.Join(lines[:len(lines)-1], "\n")
	case "different-command":
		result.Command = []string{"go", "test", "-json", "./different"}
	case "contradictory-exit":
		zero := 0
		result.ExitCode, result.Passed = &zero, true
	}
}
