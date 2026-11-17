package verification

import (
	"encoding/json"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

const complexityFinding = "Too complex"
const regressionSourceFile = "src/code.go"
const regressionSARIFArgument = "--sarif"

func TestQltyHeadDiffCannotGrandfatherReportedFindings(t *testing.T) {
	for _, level := range []string{"none", "note", "warning", "error"} {
		t.Run(level, func(t *testing.T) {
			result := regressionQuality(regressionSARIF("lint", level, "same", "Issue on changed code", 10, 0), 0)
			result.Command = []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", regressionSARIFArgument}
			compared := CompareBaseline(Report{Results: []Result{result}}, Report{Results: []Result{result}})
			if compared.Passed {
				t.Fatal("finding in Qlty's HEAD diff was grandfathered as existing debt")
			}
		})
	}
}

func TestQltyHeadDiffRejectsPreviouslyGrandfatheredEvidence(t *testing.T) {
	raw := regressionQuality(regressionSARIF("lint", "note", "same", "Issue on changed code", 10, 0), 1)
	old := CompareBaseline(Report{Results: []Result{raw}}, Report{Results: []Result{raw}})
	if !old.Passed {
		t.Fatal("full-scan debt fixture was not accepted")
	}
	result := old.Results[0]
	result.Command = []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", regressionSARIFArgument}
	if err := ValidateQltyResult(result); err == nil {
		t.Fatal("old advisory marker waived a finding in the HEAD diff")
	}
}

func TestCompareBaselineQltyRequiresFrozenRuntimeIdentity(t *testing.T) {
	output := regressionSARIF("lint", "note", "stable", "Existing issue", 10, 0)
	base := regressionQuality(output, 1)
	head := regressionQuality(output, 1)
	base.QualityIdentity, head.QualityIdentity = nil, nil
	report := CompareBaseline(Report{Results: []Result{base}}, Report{Results: []Result{head}})
	if report.Passed || !strings.Contains(report.Results[0].Error, "runtime identity") {
		t.Fatalf("Qlty evidence without frozen runtime identity was accepted: %+v", report)
	}
}

func TestCompareBaselineQltyCoordinateOnlyFindings(t *testing.T) {
	output := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"invocations":[{"executionSuccessful":true}],"results":[{"ruleId":"lint:rule","level":"note","message":{"text":"Existing issue"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"src/code.go"},"region":{"startLine":10,"startColumn":4,"endLine":10,"endColumn":20}}}]}]}]}`
	for _, tc := range []struct {
		name       string
		base, head string
		want       bool
	}{
		{"unchanged span", output, output, true},
		{"changed line", output, strings.ReplaceAll(output, `Line":10`, `Line":11`), false},
		{"changed column", output, strings.ReplaceAll(output, `"startColumn":4`, `"startColumn":5`), false},
		{"changed file", output, strings.ReplaceAll(output, regressionSourceFile, "src/new.go"), false},
		{"missing start", strings.ReplaceAll(output, `"startLine":10`, `"startLine":0`), strings.ReplaceAll(output, `"startLine":10`, `"startLine":0`), false},
		{"reversed span", strings.ReplaceAll(output, `"endColumn":20`, `"endColumn":2`), strings.ReplaceAll(output, `"endColumn":20`, `"endColumn":2`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertBaselineComparisonCase(t, regressionQuality(tc.base, 1), regressionQuality(tc.head, 1), tc.want)
		})
	}
}

func TestCompareBaselineQltyFileLevelFindings(t *testing.T) {
	// Qlty's formatter driver reports the whole file, without a region/snippet.
	output := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"invocations":[{"executionSuccessful":true}],"results":[{"ruleId":"gofmt:fmt","level":"note","message":{"text":"Incorrect formatting"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"old.go"}}}]}]}]}`
	base := regressionQuality(output, 1)
	for _, tc := range []struct {
		name, head string
		want       bool
	}{
		{"existing file finding", output, true},
		{"new file", strings.ReplaceAll(output, "old.go", "new.go"), false},
		{"changed message", strings.ReplaceAll(output, "Incorrect formatting", "New issue"), false},
		{"worsened severity", strings.ReplaceAll(output, `"note"`, `"warning"`), false},
		{"missing artifact", strings.ReplaceAll(output, "old.go", ""), false},
		{"region cannot match file-level debt", strings.ReplaceAll(output, `"artifactLocation":`, `"region":{"startLine":10},"artifactLocation":`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertBaselineComparisonCase(t, base, regressionQuality(tc.head, 1), tc.want)
		})
	}
}

func regressionResult(name, output string, exit int) Result {
	return Result{Name: name, Command: []string{"go", "test", "-json", "./..."}, Output: output, ExitCode: &exit, Passed: exit == 0}
}

func regressionSARIF(rule, level, fingerprint, message string, line, metric int) string {
	finding := map[string]any{"ruleId": rule, "level": level, "message": map[string]string{"text": message}, "locations": []any{map[string]any{"physicalLocation": map[string]any{"artifactLocation": map[string]string{"uri": regressionSourceFile}, "region": map[string]any{"startLine": line, "snippet": map[string]string{"text": "return largeFunction()"}}}}}, "properties": map[string]int{"complexity": metric}}
	if fingerprint != "" {
		finding["partialFingerprints"] = map[string]string{"primaryLocationLineHash": fingerprint}
	}
	output, _ := json.Marshal(map[string]any{"version": "2.1.0", "runs": []any{map[string]any{"tool": map[string]any{"driver": map[string]string{"name": "qlty"}}, "invocations": []any{map[string]bool{"executionSuccessful": true}}, "results": []any{finding}}}})
	return string(output)
}

func regressionQuality(output string, exit int) Result {
	r := regressionResult("qlty-check", output, exit)
	r.Command = []string{"qlty", "check", "--all", regressionSARIFArgument}
	r.QualityIdentity = regressionFixtureIdentity()
	if exit != 0 {
		r.Error = "exit status 1"
	}
	return r
}

func regressionFixtureIdentity() *model.QualityIdentity {
	return &model.QualityIdentity{Version: 1, QltyVersion: "0.668.0 fixture", QltyBinarySHA256: strings.Repeat("a", 64), ConfigurationHash: strings.Repeat("b", 64), ToolRuntimeHash: strings.Repeat("c", 64), PluginCatalogHash: strings.Repeat("d", 64), ToolRuntimePath: "/tmp/approved-runtime/tools", Tools: []model.QualityToolIdentity{{Name: "semgrep", Version: "1.179.0", Fingerprint: "c5011bfd1d76"}}}
}

func TestCompareBaselineQltyRejectsDifferentRuntimeIdentity(t *testing.T) {
	output := regressionSARIF("lint", "note", "stable", "Existing issue", 10, 0)
	base, head := regressionQuality(output, 1), regressionQuality(output, 1)
	head.QualityIdentity = regressionFixtureIdentity()
	head.QualityIdentity.QltyBinarySHA256 = strings.Repeat("d", 64)
	report := CompareBaseline(Report{Results: []Result{base}}, Report{Results: []Result{head}})
	if report.Passed || !strings.Contains(report.Results[0].Error, "runtime identity") {
		t.Fatalf("Qlty debt passed under a different scanner runtime: %+v", report)
	}
}

func TestValidateJobSARIFAcceptsValidFindingsButRequiresSuccessfulInvocations(t *testing.T) {
	findings := regressionSARIF("lint", "warning", "stable", "Existing finding", 10, 0)
	if err := ValidateJobSARIF([]byte(findings)); err != nil {
		t.Fatalf("complete SARIF with valid findings rejected: %v", err)
	}
	failed := strings.Replace(findings, `"executionSuccessful":true`, `"executionSuccessful":false`, 1)
	if err := ValidateJobSARIF([]byte(failed)); err == nil {
		t.Fatal("SARIF with unsuccessful scanner invocation accepted")
	}
}

func TestCompareBaselineSARIFDistinguishesDebtFromRegressions(t *testing.T) {
	base := regressionQuality(regressionSARIF("complexity", "warning", "stable", complexityFinding, 10, 12), 1)
	for _, tc := range []struct {
		name string
		head Result
		want bool
	}{
		{"unchanged debt moved", regressionQuality(regressionSARIF("complexity", "warning", "stable", complexityFinding, 99, 12), 1), true},
		{"new finding", regressionQuality(regressionSARIF("complexity", "warning", "new", complexityFinding, 10, 12), 1), false},
		{"new note despite successful exit", regressionQuality(regressionSARIF("complexity", "note", "new", complexityFinding, 10, 12), 0), false},
		{"new none-level finding despite successful exit", regressionQuality(regressionSARIF("complexity", "none", "new", complexityFinding, 10, 12), 0), false},
		{"severity worsened", regressionQuality(regressionSARIF("complexity", "error", "stable", complexityFinding, 10, 12), 1), false},
		{"metric worsened", regressionQuality(regressionSARIF("complexity", "warning", "stable", complexityFinding, 10, 13), 1), false},
		{"metric improved", regressionQuality(regressionSARIF("complexity", "warning", "stable", complexityFinding, 10, 11), 1), true},
		{"malformed passed report", regressionQuality("garbage", 0), false},
		{"incomplete report", regressionQuality(`{"version":"2.1.0","runs":[{}]}`, 0), false},
		{"analysis failed", regressionQuality(`{"version":"2.1.0","runs":[{"results":[],"invocations":[{"executionSuccessful":false}]}]}`, 0), false},
		{"clean head", regressionQuality(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"invocations":[{"executionSuccessful":true}],"results":[]}]}`, 0), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertBaselineComparisonCase(t, base, tc.head, tc.want)
		})
	}
}

func TestCompareBaselineAcceptsUnchangedQltySmellsExitZeroAdvisory(t *testing.T) {
	output := regressionSARIF("complexity", "warning", "stable", complexityFinding, 10, 12)
	base := regressionQltySmells(output, 0)
	head := regressionQltySmells(output, 0)
	compared := CompareBaseline(Report{Results: []Result{base}}, Report{Results: []Result{head}})
	if !compared.Passed || !AcceptedQltyBaselineAdvisory(compared.Results[0]) {
		t.Fatalf("unchanged Qlty smells were not marked as baseline advisory: %+v", compared)
	}
	if err := ValidateQltyResult(compared.Results[0]); err != nil {
		t.Fatalf("validated exit-zero baseline advisory rejected: %v", err)
	}
}

func regressionQltySmells(output string, exit int) Result {
	r := regressionResult("qlty-smells", output, exit)
	r.Command = []string{"qlty", "smells", "--all", regressionSARIFArgument}
	r.Passed = false
	r.Error = "qlty reported 1 code smells; repair findings before review"
	return r
}

func assertBaselineComparisonCase(t *testing.T, base, head Result, want bool) {
	t.Helper()
	report := CompareBaseline(Report{Results: []Result{base}}, Report{Results: []Result{head}})
	if report.Passed != want || report.Results[0].Passed != want {
		t.Fatalf("passed=%v result=%+v want=%v", report.Passed, report.Results[0], want)
	}
	wantAdvisory := want && *head.ExitCode != 0
	if got := AcceptedQltyBaselineAdvisory(report.Results[0]); got != wantAdvisory {
		t.Fatalf("accepted Qlty advisory=%v want=%v", got, wantAdvisory)
	}
	if wantAdvisory && !strings.Contains(report.Results[0].Output, "baseline_comparison") {
		t.Fatal("preexisting debt lost comparison metadata")
	}
	if wantAdvisory {
		if err := ValidateQltyResult(report.Results[0]); err != nil {
			t.Fatalf("validated baseline advisory rejected: %v", err)
		}
	}
	if head.Output != report.Results[0].Output && !strings.Contains(report.Results[0].Output, head.Output) {
		t.Fatal("raw evidence lost")
	}
}

func TestValidateQltyResultRequiresRawCompleteSarif(t *testing.T) {
	valid := regressionQuality(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"invocations":[{"executionSuccessful":true}],"results":[]}]}`, 0)
	if err := ValidateQltyResult(valid); err != nil {
		t.Fatalf("valid clean Qlty SARIF rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Result)
	}{
		{"malformed report", func(result *Result) { result.Output = "not SARIF" }},
		{"missing scanner execution evidence", func(result *Result) { result.Output = `{"version":"2.1.0","runs":[{"results":[]}]}` }},
		{"missing successful invocation", func(result *Result) {
			result.Output = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"results":[]}]}`
		}},
		{"finding despite success", func(result *Result) {
			result.Output = regressionSARIF("complexity", "warning", "stable", complexityFinding, 3, 12)
		}},
		{"truncated report", func(result *Result) { result.Truncated = true }},
		{"missing exit status", func(result *Result) { result.ExitCode = nil }},
		{"failed process marked passed", func(result *Result) { code := 1; result.ExitCode, result.Passed = &code, true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := valid
			tc.mutate(&result)
			if err := ValidateQltyResult(result); err == nil {
				t.Fatal("invalid or incomplete Qlty evidence accepted")
			}
		})
	}
}

func TestCompareBaselineSARIFWithoutFingerprintsIgnoresMovedLine(t *testing.T) {
	base := regressionQuality(regressionSARIF("complexity", "warning", "", "Too   complex", 10, 12), 1)
	head := regressionQuality(regressionSARIF("complexity", "warning", "", complexityFinding, 80, 12), 1)
	report := CompareBaseline(Report{Results: []Result{base}}, Report{Results: []Result{head}})
	if !report.Passed {
		t.Fatalf("moved finding blocked: %+v", report)
	}
}

func TestCompareBaselineFingerprintCannotHideFindingInNewFile(t *testing.T) {
	output := regressionSARIF("complexity", "note", "shared", complexityFinding, 10, 12)
	base := regressionQuality(output, 1)
	head := regressionQuality(strings.ReplaceAll(output, regressionSourceFile, "src/new.go"), 1)
	if report := CompareBaseline(Report{Results: []Result{base}}, Report{Results: []Result{head}}); report.Passed {
		t.Fatal("reused fingerprint hid a finding introduced in a different file")
	}
}

func TestCompareBaselineDoesNotHideAdditionalIdenticalFinding(t *testing.T) {
	output := regressionSARIF("complexity", "warning", "same", complexityFinding, 1, 12)
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
		{"same go failure remains blocking", old, old, false},
		{"new go failure", old, newer, false},
		{"unfinished go run", old, `{"Action":"run","Package":"example","Test":"TestOld"}`, false},
		{"go package failed without test", old, `{"Action":"fail","Package":"example"}`, false},
		{"same junit failure remains blocking", junit, junit, false},
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
	output := regressionSARIF("complexity", "warning", "stable", complexityFinding, 10, 12)
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
	severe := regressionSARIF("complexity", "error", "same", complexityFinding, 10, 12)
	ordinary := regressionSARIF("complexity", "warning", "same", complexityFinding, 20, 13)
	base := regressionQuality(combine(severe, ordinary), 1)
	improved := regressionSARIF("complexity", "warning", "same", complexityFinding, 20, 12)
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
