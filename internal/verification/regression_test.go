package verification

import (
	"encoding/json"
	"strings"
	"testing"
)

func regressionResult(name, output string, exit int) Result {
	return Result{Name: name, Command: []string{"go", "test", "-json", "./..."}, Output: output, ExitCode: &exit, Passed: exit == 0}
}

func regressionSARIF(rule, level, fingerprint, message string, line, metric int) string {
	finding := map[string]any{"ruleId": rule, "level": level, "message": map[string]string{"text": message}, "locations": []any{map[string]any{"physicalLocation": map[string]any{"artifactLocation": map[string]string{"uri": "src/code.go"}, "region": map[string]any{"startLine": line, "snippet": map[string]string{"text": "return largeFunction()"}}}}}, "properties": map[string]int{"complexity": metric}}
	if fingerprint != "" {
		finding["partialFingerprints"] = map[string]string{"primaryLocationLineHash": fingerprint}
	}
	output, _ := json.Marshal(map[string]any{"version": "2.1.0", "runs": []any{map[string]any{"results": []any{finding}}}})
	return string(output)
}

func regressionQuality(output string, exit int) Result {
	r := regressionResult("qlty-check", output, exit)
	r.Command = []string{"qlty", "check", "--all", "--sarif"}
	if exit != 0 {
		r.Error = "exit status 1"
	}
	return r
}

func TestCompareBaselineSARIFDistinguishesDebtFromRegressions(t *testing.T) {
	base := regressionQuality(regressionSARIF("complexity", "warning", "stable", "Too complex", 10, 12), 1)
	for _, tc := range []struct {
		name string
		head Result
		want bool
	}{
		{"unchanged debt moved", regressionQuality(regressionSARIF("complexity", "warning", "stable", "Too complex", 99, 12), 1), true},
		{"new finding", regressionQuality(regressionSARIF("complexity", "warning", "new", "Too complex", 10, 12), 1), false},
		{"severity worsened", regressionQuality(regressionSARIF("complexity", "error", "stable", "Too complex", 10, 12), 1), false},
		{"metric worsened", regressionQuality(regressionSARIF("complexity", "warning", "stable", "Too complex", 10, 13), 1), false},
		{"metric improved", regressionQuality(regressionSARIF("complexity", "warning", "stable", "Too complex", 10, 11), 1), true},
		{"malformed passed report", regressionQuality("garbage", 0), false},
		{"incomplete report", regressionQuality(`{"version":"2.1.0","runs":[{}]}`, 0), false},
		{"analysis failed", regressionQuality(`{"version":"2.1.0","runs":[{"results":[],"invocations":[{"executionSuccessful":false}]}]}`, 0), false},
		{"clean head", regressionQuality(`{"version":"2.1.0","runs":[{"results":[]}]}`, 0), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := CompareBaseline(Report{Results: []Result{base}}, Report{Results: []Result{tc.head}})
			if report.Passed != tc.want || report.Results[0].Passed != tc.want {
				t.Fatalf("passed=%v result=%+v want=%v", report.Passed, report.Results[0], tc.want)
			}
			if tc.want && *tc.head.ExitCode != 0 && !strings.Contains(report.Results[0].Output, "baseline_comparison") {
				t.Fatal("preexisting debt lost comparison metadata")
			}
			if tc.head.Output != report.Results[0].Output && !strings.Contains(report.Results[0].Output, tc.head.Output) {
				t.Fatal("raw evidence lost")
			}
		})
	}
}

func TestCompareBaselineSARIFWithoutFingerprintsIgnoresMovedLine(t *testing.T) {
	base := regressionQuality(regressionSARIF("complexity", "warning", "", "Too   complex", 10, 12), 1)
	head := regressionQuality(regressionSARIF("complexity", "warning", "", "Too complex", 80, 12), 1)
	report := CompareBaseline(Report{Results: []Result{base}}, Report{Results: []Result{head}})
	if !report.Passed {
		t.Fatalf("moved finding blocked: %+v", report)
	}
}

func TestCompareBaselineDoesNotHideAdditionalIdenticalFinding(t *testing.T) {
	output := regressionSARIF("complexity", "warning", "same", "Too complex", 1, 12)
	var sarif map[string]any
	_ = json.Unmarshal([]byte(output), &sarif)
	run := sarif["runs"].([]any)[0].(map[string]any)
	finding := run["results"].([]any)[0]
	run["results"] = []any{finding, finding}
	duplicate, _ := json.Marshal(sarif)
	report := CompareBaseline(Report{Results: []Result{regressionQuality(output, 1)}}, Report{Results: []Result{regressionQuality(string(duplicate), 1)}})
	if report.Passed {
		t.Fatal("additional identical finding accepted")
	}
}

func TestCompareBaselineStructuredTestFailures(t *testing.T) {
	old := `{"Action":"run","Package":"example","Test":"TestOld"}
{"Action":"fail","Package":"example","Test":"TestOld"}
{"Action":"fail","Package":"example"}`
	newer := strings.ReplaceAll(old, "TestOld", "TestNew")
	junit := `<testsuites tests="2" failures="1"><testsuite tests="2" failures="1"><testcase classname="suite" name="old"><failure message="fails"/></testcase><testcase classname="suite" name="ok"/></testsuite></testsuites>`
	for _, tc := range []struct {
		name, base, head string
		want             bool
	}{
		{"same go failure", old, old, true},
		{"new go failure", old, newer, false},
		{"unfinished go run", old, `{"Action":"run","Package":"example","Test":"TestOld"}`, false},
		{"go package failed without test", old, `{"Action":"fail","Package":"example"}`, false},
		{"same junit failure", junit, junit, true},
		{"new junit failure", junit, strings.ReplaceAll(junit, `name="old"`, `name="new"`), false},
		{"incomplete junit", junit, `<testsuite failures="1"></testsuite>`, false},
		{"opaque errors", "FAIL", "FAIL", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := regressionResult("tests", tc.base, 1)
			head := regressionResult("tests", tc.head, 1)
			head.Error = "exit status 1"
			report := CompareBaseline(Report{Results: []Result{base}}, Report{Results: []Result{head}})
			if report.Passed != tc.want {
				t.Fatalf("passed=%v want=%v %+v", report.Passed, tc.want, report)
			}
		})
	}
}

func TestCompareBaselineRejectsInfrastructureAndMissingChecks(t *testing.T) {
	output := regressionSARIF("complexity", "warning", "stable", "Too complex", 10, 12)
	base := regressionQuality(output, 1)
	for _, tc := range []struct {
		name   string
		mutate func(*Result)
	}{
		{"timeout", func(r *Result) { r.Error = "context deadline exceeded" }},
		{"tool crash", func(r *Result) { code := 139; r.ExitCode = &code; r.Error = "exit status 139" }},
		{"missing exit", func(r *Result) { r.ExitCode = nil }},
		{"truncated", func(r *Result) { r.Truncated = true }},
		{"different command", func(r *Result) { r.Command = append(r.Command, "--new-config") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			head := base
			tc.mutate(&head)
			report := CompareBaseline(Report{Results: []Result{base}}, Report{Results: []Result{head}})
			if report.Passed {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
	if report := CompareBaseline(Report{Results: []Result{base}}, Report{}); report.Passed {
		t.Fatal("omitted baseline check accepted")
	}
}

func TestCompareBaselinePassingHeadNeedsNoUsableBaseline(t *testing.T) {
	head := regressionResult("tests", "PASS", 0)
	report := CompareBaseline(Report{Results: []Result{regressionResult("tests", "crashed", 2)}}, Report{Results: []Result{head}})
	if !report.Passed {
		t.Fatalf("passing head rejected: %+v", report)
	}
	if !CompareBaseline(Report{}, Report{Results: []Result{head}}).Passed {
		t.Fatal("passing new check rejected")
	}
}

func TestCompareBaselineSARIFMatchesDuplicateIdentitiesRegardlessOfOrder(t *testing.T) {
	combine := func(first, second string) string {
		var a, b map[string]any
		_ = json.Unmarshal([]byte(first), &a)
		_ = json.Unmarshal([]byte(second), &b)
		ar := a["runs"].([]any)[0].(map[string]any)
		br := b["runs"].([]any)[0].(map[string]any)
		ar["results"] = append(ar["results"].([]any), br["results"].([]any)...)
		output, _ := json.Marshal(a)
		return string(output)
	}
	severe := regressionSARIF("complexity", "error", "same", "Too complex", 10, 12)
	ordinary := regressionSARIF("complexity", "warning", "same", "Too complex", 20, 13)
	base := regressionQuality(combine(severe, ordinary), 1)
	improved := regressionSARIF("complexity", "warning", "same", "Too complex", 20, 12)
	head := regressionQuality(combine(improved, severe), 1)
	if result := CompareBaseline(Report{Results: []Result{base}}, Report{Results: []Result{head}}); !result.Passed {
		t.Fatalf("equivalent findings reordered: %+v", result)
	}
}

func TestCompareBaselineSARIFMissingLocationCannotBeMatchedByMessageAlone(t *testing.T) {
	result := regressionQuality(`{"version":"2.1.0","runs":[{"results":[{"ruleId":"complexity","message":{"text":"Too complex"}}]}]}`, 1)
	if CompareBaseline(Report{Results: []Result{result}}, Report{Results: []Result{result}}).Passed {
		t.Fatal("finding without stable identity grandfathered")
	}
}
