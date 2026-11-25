package verification

import (
	"encoding/json"
	"testing"
)

// A successful process cannot certify a report with missing or malformed
// scanner diagnostics. Both invocation notification collections follow SARIF.
func TestSARIFMalformedNotificationsCannotCertifyCleanEvidence(t *testing.T) {
	for _, field := range []string{"toolExecutionNotifications", "toolConfigurationNotifications"} {
		for _, tc := range []struct{ name, raw string }{
			{"null collection", `null`},
			{"object collection", `{}`},
			{"null notification", `[null]`},
			{"scalar notification", `[7]`},
			{"missing message", `[{"level":"note"}]`},
			{"null message", `[{"message":null}]`},
			{"scalar message", `[{"message":"lost diagnostics"}]`},
			{"empty message", `[{"message":{}}]`},
			{"markdown without text or id", `[{"message":{"markdown":"diagnostic"}}]`},
			{"nonstring text", `[{"message":{"text":7}}]`},
			{"null text", `[{"message":{"text":null}}]`},
			{"null id with text", `[{"message":{"text":"diagnostic","id":null}}]`},
			{"null markdown with text", `[{"message":{"text":"diagnostic","markdown":null}}]`},
			{"scalar arguments", `[{"message":{"text":"diagnostic","arguments":7}}]`},
			{"null arguments", `[{"message":{"text":"diagnostic","arguments":null}}]`},
			{"null argument", `[{"message":{"text":"diagnostic","arguments":[null]}}]`},
			{"scalar properties", `[{"message":{"text":"diagnostic","properties":7}}]`},
			{"null properties", `[{"message":{"text":"diagnostic","properties":null}}]`},
			{"unknown message field", `[{"message":{"text":"diagnostic","lostError":true}}]`},
			{"null level", `[{"level":null,"message":{"text":"diagnostic"}}]`},
			{"empty level", `[{"level":"","message":{"text":"diagnostic"}}]`},
			{"unknown level", `[{"level":"info","message":{"text":"diagnostic"}}]`},
			{"reported tool error", `[{"level":"error","message":{"text":"scanner failed"}}]`},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) { assertNotificationEvidence(t, field, tc.raw, true) })
		}
	}
}

func TestSARIFValidNotificationsPreserveCleanEvidence(t *testing.T) {
	for _, field := range []string{"toolExecutionNotifications", "toolConfigurationNotifications"} {
		for _, raw := range []string{
			`[]`,
			`[{"message":{"text":"scanner completed"}}]`,
			`[{"level":"note","message":{"text":"Successfully ran gitleaks in 1.00s [fixture]"}}]`,
			`[{"level":"none","message":{"text":"scanner completed"}}]`,
			`[{"level":"warning","message":{"text":"diagnostic advisory"}}]`,
			`[{"level":"note","message":{"id":"completed","arguments":["scanner"]}}]`,
			`[{"level":"note","message":{"text":"","markdown":"formatted diagnostic"}}]`,
			`[{"message":{"text":"scanner completed","arguments":[],"properties":{"custom":null}}}]`,
		} {
			assertNotificationEvidence(t, field, raw, false)
		}
	}
}

func TestCleanFullQltyScanRequiresUsableBaseline(t *testing.T) {
	head := regressionQuality(sarifNotificationReport(t, "toolExecutionNotifications", `[]`), 0)
	failedWithoutExitError := head
	failedWithoutExitError.Passed = false
	for _, base := range []Report{
		{},
		{Results: []Result{regressionQuality("not SARIF", 0)}},
		{Results: []Result{regressionQuality(head.Output, 2)}},
		{Results: []Result{regressionQuality(head.Output, 1)}},
		{Results: []Result{failedWithoutExitError}},
	} {
		if report := CompareBaseline(base, Report{Passed: true, Results: []Result{head}}); report.Passed {
			t.Errorf("clean full scan waived missing or unusable baseline: %+v", report)
		}
	}
}

func TestCleanQltyHeadDiffDoesNotRequireScanBaseline(t *testing.T) {
	head := regressionQuality(sarifNotificationReport(t, "toolExecutionNotifications", `[]`), 0)
	head.Command = []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"}
	if report := CompareBaseline(Report{}, Report{Passed: true, Results: []Result{head}}); !report.Passed {
		t.Fatalf("self-contained HEAD diff acquired a full-scan baseline requirement: %+v", report)
	}
}

func assertNotificationEvidence(t *testing.T, field, raw string, wantError bool) {
	t.Helper()
	output := sarifNotificationReport(t, field, raw)
	assertSARIFEvidence(t, output, wantError)
}

func TestSARIFComparisonsRequireCompletedToolEvidence(t *testing.T) {
	for _, output := range []string{
		`{"version":"2.1.0","runs":[{"invocations":[{"executionSuccessful":true}],"results":[]}]}`,
		`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"results":[]}]}`,
		`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"invocations":[],"results":[]}]}`,
		`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"invocations":[{}],"results":[]}]}`,
		`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty"}},"invocations":[{"executionSuccessful":null}],"results":[]}]}`,
	} {
		assertSARIFEvidence(t, output, true)
	}
}

func assertSARIFEvidence(t *testing.T, output string, wantError bool) {
	t.Helper()
	result := regressionQuality(output, 0)
	for name, err := range map[string]error{
		"runner":     validateJobSARIFEvidence([]byte(output)),
		"acceptance": ValidateQltyResult(result),
	} {
		if (err != nil) != wantError {
			t.Errorf("%s error=%v, wantError=%t; %s", name, err, wantError, output)
		}
	}
	clean := regressionQuality(sarifNotificationReport(t, "toolExecutionNotifications", `[]`), 0)
	for _, pair := range [][2]Result{{clean, result}, {result, clean}} {
		report := CompareBaseline(Report{Passed: true, Results: []Result{pair[0]}}, Report{Passed: true, Results: []Result{pair[1]}})
		if report.Passed == wantError {
			t.Errorf("baseline comparison accepted invalid evidence or rejected valid evidence: %+v", report)
		}
	}
}

func sarifNotificationReport(t *testing.T, field, raw string) string {
	t.Helper()
	report := map[string]any{"version": "2.1.0", "runs": []any{map[string]any{
		"tool":        map[string]any{"driver": map[string]string{"name": "qlty"}},
		"invocations": []any{map[string]any{"executionSuccessful": true, field: json.RawMessage(raw)}},
		"results":     []any{},
	}}}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
