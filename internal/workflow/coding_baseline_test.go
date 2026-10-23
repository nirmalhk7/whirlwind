package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

type codingBaselineOutcome struct{ name, action string }

func codingBaselineResult(tests ...codingBaselineOutcome) verification.Result {
	events := []string{}
	exit, final := 0, "pass"
	for _, test := range tests {
		for _, action := range []string{"run", test.action} {
			encoded, _ := json.Marshal(map[string]string{"Action": action, "Package": "example", "Test": test.name})
			events = append(events, string(encoded))
		}
		if test.action == "fail" {
			exit, final = 1, "fail"
		}
	}
	encoded, _ := json.Marshal(map[string]string{"Action": final, "Package": "example"})
	return verification.Result{Name: "tests", Command: []string{"go", "test", "-json", "./..."}, Output: strings.Join(append(events, string(encoded)), "\n"), Passed: exit == 0, ExitCode: &exit}
}

func codingBaselineFixture(t *testing.T, result verification.Result) *codingCheckRun {
	t.Helper()
	e, task, _ := testEngine(t)
	a := &agentAttempt{agentRun: &agentRun{Engine: e, task: task, input: provider.Input{UnitID: "one"}}}
	return &codingCheckRun{attempt: a, check: model.Check{Name: "tests"}, entry: tddCheck{Production: "implemented", Tests: "regression-tests"}, result: result, report: verification.Report{Passed: result.Passed, Results: []verification.Result{result}}}
}

func saveCodingBaselineRed(t *testing.T, run *codingCheckRun, id string, result verification.Result) {
	t.Helper()
	ctx := context.Background()
	if err := run.attempt.Store.Put(ctx, "sessions", id, model.Attempt{AttemptID: id, Task: run.attempt.task.ID, Role: "code"}, 0); err != nil {
		t.Fatal(err)
	}
	artifact, err := run.attempt.Store.Capture(ctx, id, "tdd_check:tests", "text/plain", []byte(result.Output), store.MessagePayloadLimit, false)
	if err != nil {
		t.Fatal(err)
	}
	result.Output, result.OutputArtifact = "", artifact.ID
	prior := tddCheck{Task: run.attempt.task.ID, PlanVersion: run.attempt.task.PlanVersion, Unit: "one", CheckID: "tests", Phase: "red", Tests: run.entry.Tests, Production: "original", RawResult: &result, OutputArtifact: artifact.ID}
	if err := run.attempt.Store.Put(ctx, "tdd_checks", id, prior, 0); err != nil {
		t.Fatal(err)
	}
}

func assertCodingBaselinePhase(t *testing.T, run *codingCheckRun, base verification.Result, want string, gate bool) {
	t.Helper()
	origin := codingOrigin{Production: "original", Tests: "original-tests"}
	phase, err := run.phaseAgainstBaseline(context.Background(), origin, verification.Report{Passed: base.Passed, Results: []verification.Result{base}})
	if err != nil || phase != want || run.gatePassed != gate {
		t.Fatalf("phase=%s gate=%v error=%v; want phase=%s gate=%v", phase, run.gatePassed, err, want, gate)
	}
}

func TestCodingBaselinePassingAggregateRequiresRegressionPass(t *testing.T) {
	base := codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"})
	red := codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"}, codingBaselineOutcome{"TestRegression", "fail"})
	for _, outcome := range []string{"skip", "missing", "opaque"} {
		t.Run(outcome, func(t *testing.T) {
			green := codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"})
			if outcome == "skip" {
				green = codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"}, codingBaselineOutcome{"TestRegression", "skip"})
			}
			if outcome == "opaque" {
				green.Output = "PASS"
			}
			run := codingBaselineFixture(t, green)
			saveCodingBaselineRed(t, run, "red", red)
			assertCodingBaselinePhase(t, run, base, "invalid", true)
		})
	}
}

func TestCodingBaselineLatestRedCannotFallBackToOlderFailureSet(t *testing.T) {
	base := codingBaselineResult(codingBaselineOutcome{"TestOld", "fail"})
	green := codingBaselineResult(codingBaselineOutcome{"TestOld", "fail"}, codingBaselineOutcome{"TestFirst", "pass"}, codingBaselineOutcome{"TestSecond", "skip"})
	run := codingBaselineFixture(t, green)
	saveCodingBaselineRed(t, run, "a-older", codingBaselineResult(codingBaselineOutcome{"TestOld", "fail"}, codingBaselineOutcome{"TestFirst", "fail"}, codingBaselineOutcome{"TestSecond", "skip"}))
	saveCodingBaselineRed(t, run, "z-latest", codingBaselineResult(codingBaselineOutcome{"TestOld", "fail"}, codingBaselineOutcome{"TestFirst", "fail"}, codingBaselineOutcome{"TestSecond", "fail"}))
	assertCodingBaselinePhase(t, run, base, "invalid", true)
	if !run.entry.BaselineAccepted {
		t.Fatal("unchanged baseline debt lost its advisory status")
	}
}

func TestCodingBaselineExplicitRegressionPassSupportsCleanAndDebtReports(t *testing.T) {
	for _, old := range []string{"pass", "fail"} {
		t.Run(old, func(t *testing.T) {
			base := codingBaselineResult(codingBaselineOutcome{"TestOld", old})
			red := codingBaselineResult(codingBaselineOutcome{"TestOld", old}, codingBaselineOutcome{"TestRegression", "fail"})
			green := codingBaselineResult(codingBaselineOutcome{"TestOld", old}, codingBaselineOutcome{"TestRegression", "pass"})
			run := codingBaselineFixture(t, green)
			saveCodingBaselineRed(t, run, "red", red)
			assertCodingBaselinePhase(t, run, base, "green", true)
			if run.entry.BaselineAccepted != (old == "fail") || *run.result.ExitCode != *green.ExitCode {
				t.Fatal("raw result or baseline debt status changed")
			}
		})
	}
}

func TestCodingBaselinePreservesTestFirstRedAndNonRegressionGate(t *testing.T) {
	base := codingBaselineResult(codingBaselineOutcome{"TestOld", "fail"})
	red := codingBaselineResult(codingBaselineOutcome{"TestOld", "fail"}, codingBaselineOutcome{"TestRegression", "fail"})
	run := codingBaselineFixture(t, red)
	run.entry.Production = "original"
	assertCodingBaselinePhase(t, run, base, "red", false)
	run = codingBaselineFixture(t, base)
	assertCodingBaselinePhase(t, run, base, "invalid", true)
}
