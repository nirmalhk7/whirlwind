package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/verification"
)

// Scanner output is controlled here to exercise the real isolated runner and
// acceptance handoff. Installed Qlty compatibility has its own native test.
func cleanQltyFixtureCheck(t *testing.T) model.Check {
	t.Helper()
	return guardedQltyFixtureCheck(t, "")
}

func guardedQltyFixtureCheck(t *testing.T, candidateGuard string) model.Check {
	t.Helper()
	const output = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"qlty","version":"fixture","semanticVersion":"1.0.0"}},"invocations":[{"executionSuccessful":true}],"results":[]}]}`
	tool := writeQltyFixture(t, output, "0", candidateGuard)
	return model.Check{Name: "quality", Command: []string{tool, "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"}, Mode: "autofix", ReportFormat: "sarif", TimeoutSeconds: 5}
}

func writeQltyFixture(t *testing.T, sarif, exit, candidateGuard string) string {
	t.Helper()
	tool := filepath.Join(t.TempDir(), "qlty")
	const setup = `#!/bin/sh
set -eu
if [ "$1" = config ]; then
  printf '%s\n' 'plugins: {}'
  exit 0
fi
root="$HOME/.qlty/cache/tools/fixture"
if [ ! -f "$root/1.0-12345678.done" ]; then
  mkdir -p "$root/1.0-12345678"
  printf '%s\n' 'fixture runtime' > "$root/1.0-12345678/scanner"
  touch "$root/1.0-12345678.done"
else
`
	script := setup + candidateGuard + ":\nfi\nprintf '%s\n' '" + sarif + "'\nexit " + exit + "\n"
	requireWorkflowSuccess(t, os.WriteFile(tool, []byte(script), 0700))
	return tool
}

func configurePlannerFixtureChecks(t *testing.T, e *Engine, projectID string, checks []model.Check) {
	t.Helper()
	if len(checks) > 0 {
		configureFixtureChecks(t, e, projectID, checks)
	}
	e.PlansRoot = t.TempDir()
}

func includeFixturePlanQlty(t *testing.T, task *model.ExecutionPlan, checks ...model.Check) {
	t.Helper()
	if len(checks) == 0 {
		checks = []model.Check{{Name: "qlty-check", Command: []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"}, Mode: "autofix", ReportFormat: "sarif"}}
	}
	task.Plan.Checks = append(task.Plan.Checks, checks...)
	if err := os.WriteFile(task.PlanDocument.Path, []byte(RenderPlanDocument(*task.Plan)), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptanceRejectsPolicyWithoutQltyDespitePassingReview(t *testing.T) {
	e, task, _, _ := acceptedDeliveryFixtureOptions(t, nil, nil, nil, false)
	_, _, accepted, err := e.LoadAcceptedReviewReport(context.Background(), task.ID)
	if accepted || err == nil || !strings.Contains(err.Error(), "Qlty check evidence is required") {
		t.Fatalf("passing ordinary checks and review bypassed mandatory Qlty: accepted=%t err=%v", accepted, err)
	}
}

func TestAcceptanceRequiresQltyCheckEvidence(t *testing.T) {
	e, _, _ := testEngine(t)
	zero := 0
	for _, tc := range []struct {
		name   string
		report verification.Report
	}{
		{"empty report", verification.Report{Passed: true}},
		{"ordinary checks only", verification.Report{Passed: true, Results: []verification.Result{{Name: "tests", Command: []string{"go", "test", "./..."}, Passed: true, ExitCode: &zero}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := e.validateQltyEvidence(context.Background(), model.ExecutionPlan{}, tc.report); err == nil || !strings.Contains(err.Error(), "Qlty check evidence is required") {
				t.Fatalf("code accepted without mandatory Qlty check evidence: %v", err)
			}
		})
	}
}
