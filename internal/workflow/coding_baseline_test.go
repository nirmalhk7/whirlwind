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

const (
	codingBaselineFixtureOldTests = "old-tests"
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
	base := codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"})
	green := codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"}, codingBaselineOutcome{"TestFirst", "pass"}, codingBaselineOutcome{"TestSecond", "skip"})
	run := codingBaselineFixture(t, green)
	saveCodingBaselineRed(t, run, "a-older", codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"}, codingBaselineOutcome{"TestFirst", "fail"}, codingBaselineOutcome{"TestSecond", "skip"}))
	saveCodingBaselineRed(t, run, "z-latest", codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"}, codingBaselineOutcome{"TestFirst", "fail"}, codingBaselineOutcome{"TestSecond", "fail"}))
	assertCodingBaselinePhase(t, run, base, "invalid", true)
	if run.entry.BaselineAccepted {
		t.Fatal("clean baseline was recorded as accepted test debt")
	}
}

func TestCodingBaselineExplicitRegressionPassDoesNotWaiveExistingFailures(t *testing.T) {
	for _, old := range []string{"pass", "fail"} {
		t.Run(old, func(t *testing.T) {
			base := codingBaselineResult(codingBaselineOutcome{"TestOld", old})
			red := codingBaselineResult(codingBaselineOutcome{"TestOld", old}, codingBaselineOutcome{"TestRegression", "fail"})
			green := codingBaselineResult(codingBaselineOutcome{"TestOld", old}, codingBaselineOutcome{"TestRegression", "pass"})
			run := codingBaselineFixture(t, green)
			saveCodingBaselineRed(t, run, "red", red)
			phase, gate := "green", true
			if old == "fail" {
				phase, gate = "invalid", false
			}
			assertCodingBaselinePhase(t, run, base, phase, gate)
			if run.entry.BaselineAccepted || *run.result.ExitCode != *green.ExitCode {
				t.Fatal("failed mandatory check was waived or raw exit evidence changed")
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
	assertCodingBaselinePhase(t, run, base, "invalid", false)
}

func TestCodingInvalidRegressionNeverClaimsACompletedPass(t *testing.T) {
	base := codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"})
	red := codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"}, codingBaselineOutcome{"TestRegression", "fail"})
	green := codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"})
	run := codingBaselineFixture(t, green)
	saveCodingBaselineRed(t, run, "recorded-red", red)

	var err error
	run.phase, err = run.phaseAgainstBaseline(t.Context(), codingOrigin{Production: "original", Tests: codingBaselineFixtureOldTests},
		verification.Report{Passed: base.Passed, Results: []verification.Result{base}})
	requireWorkflowSuccess(t, err)
	got := run.output()
	if got.Phase != "invalid" || got.Passed || got.ExitCode == nil || *got.ExitCode != 0 ||
		!strings.Contains(got.ValidationError, "omitted regression test") || !strings.Contains(got.ValidationError, "example/TestRegression") {
		t.Fatalf("an aggregate pass was mistaken for valid regression evidence: %+v", got)
	}
}

func TestCodingInvalidRegressionRetainsRawResultAndSpecificDiagnostic(t *testing.T) {
	ctx := t.Context()
	base := codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"})
	red := codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"}, codingBaselineOutcome{"TestRegression", "fail"})
	green := codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"}, codingBaselineOutcome{"TestRegression", "skip"})
	run := codingBaselineFixture(t, green)
	saveCodingBaselineRed(t, run, "recorded-red", red)
	a := run.attempt
	a.id = "candidate-attempt"
	run.current = model.Project{ID: a.task.ProjectID, PolicyHash: "fixture-policy"}
	run.check.Command = base.Command
	run.entry.Snapshot = "stored-candidate"
	run.entry.Files = map[string]string{"main.go": "implemented", "main_test.go": "regression"}
	requireWorkflowSuccess(t, a.Store.Put(ctx, "sessions", a.id, model.Attempt{AttemptID: a.id, Task: a.task.ID, Role: "code"}, 0))
	origin := codingOrigin{Production: "original", Tests: codingBaselineFixtureOldTests}
	requireWorkflowSuccess(t, a.Store.Put(ctx, "tdd_origins", codingOriginKey(a.task, a.input.UnitID), origin, 0))
	baseline := verification.Report{Passed: true, Results: []verification.Result{base}}
	// Use the persisted baseline precisely as the real check would; no
	// platform-specific sandbox or agent is needed to exercise its gate.
	key := qualityBaselineKey(a.task, run.current.PolicyHash, structuredChecks([]model.Check{run.check}))
	requireWorkflowSuccess(t, a.Store.Put(ctx, "quality_baselines", key, baseline, 0))
	requireWorkflowSuccess(t, run.persist(ctx))
	rows, err := a.Store.List(ctx, "tdd_checks")
	requireWorkflowSuccess(t, err)
	saved, err := store.Decode[tddCheck](rows[len(rows)-1])
	requireWorkflowSuccess(t, err)
	if saved.Phase != "invalid" || saved.Passed || saved.RawResult == nil || !saved.RawResult.Passed ||
		saved.RawResult.ExitCode == nil || *saved.RawResult.ExitCode != 0 || !strings.Contains(saved.ValidationError, "did not explicitly pass") {
		t.Fatalf("stored invalid check lost the reason or raw process result: %+v", saved)
	}
	_, artifact, err := a.Store.Artifact(ctx, saved.OutputArtifact)
	requireWorkflowSuccess(t, err)
	if string(artifact) != green.Output {
		t.Fatal("validation diagnosis changed the retained raw test evidence")
	}
	if pairs := currentTDDPairs(rows, a.task, run.entry.Files); len(pairs) != 0 {
		t.Fatal("invalid evidence became eligible as a reviewed red/green pair")
	}
}

func TestCodingInvalidRegressionExplainsMissingRedAndRunnerProblems(t *testing.T) {
	base := codingBaselineResult(codingBaselineOutcome{"TestOld", "pass"})
	baseline := verification.Report{Passed: true, Results: []verification.Result{base}}
	origin := codingOrigin{Production: "original", Tests: codingBaselineFixtureOldTests}
	for _, tc := range []struct {
		name, want string
		run        func() verification.Result
	}{
		{"missing recorded red", "no matching test-first red", func() verification.Result { return base }},
		{"runner never started", "runtime bootstrap failed", func() verification.Result {
			return verification.Result{Name: base.Name, Command: base.Command, Error: "runtime bootstrap failed"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := codingBaselineFixture(t, tc.run())
			var err error
			run.phase, err = run.phaseAgainstBaseline(t.Context(), origin, baseline)
			requireWorkflowSuccess(t, err)
			got := run.output()
			if got.Phase != "invalid" || got.Passed || !strings.Contains(got.ValidationError, tc.want) {
				t.Fatalf("invalid check lost its reason: %+v", got)
			}
		})
	}
}
