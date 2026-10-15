package verification

import (
	"context"
	"errors"
	"testing"

	"whirlwind/internal/model"
)

// TestChecksExecuteAndFailureBlocksAcceptance verifies project check commands execute and any failed check makes the report fail.
func TestChecksExecuteAndFailureBlocksAcceptance(t *testing.T) {
	r := Run(context.Background(), t.TempDir(), []model.Check{{Name: "pass", Command: []string{"sh", "-c", "exit 0"}}, {Name: "fail", Command: []string{"sh", "-c", "echo diagnostic; exit 2"}}})
	if r.Passed || len(r.Results) != 2 || r.Results[1].Output != "diagnostic\n" {
		t.Fatalf("%+v", r)
	}
}

func TestInterruptedSuiteRetainsCompletedChecksWithoutPassing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := []Result{}
	report, err := RunObserved(ctx, t.TempDir(), []model.Check{{Name: "first", Command: []string{"sh", "-c", "printf completed"}}, {Name: "second", Command: []string{"sh", "-c", "exit 0"}}}, func(result Result) error {
		observed = append(observed, result)
		cancel()
		return nil
	})
	if report.Passed || !errors.Is(err, context.Canceled) || len(observed) != 1 || len(report.Results) != 1 || !report.Results[0].Passed || report.Results[0].Output != "completed" {
		t.Fatal("interrupted suite mislabeled or completed check lost", report, err)
	}
}

// TestRiskKeepsBaselineAndSelectsHighest verifies risk selection retains baseline checks and selects the highest matching level.
func TestRiskKeepsBaselineAndSelectsHighest(t *testing.T) {
	p := model.Policy{Checks: []model.Check{{Name: "baseline"}}, RiskRules: []model.RiskRule{{Level: "high", Paths: []string{"auth/**"}, Checks: []model.Check{{Name: "security"}}}, {Level: "critical", Paths: []string{"auth/permissions.go"}}}}
	risk, c := Select(p, []string{"auth/permissions.go"})
	if risk != "critical" || len(c) != 2 || c[0].Name != "baseline" {
		t.Fatalf("%s %+v", risk, c)
	}
}

// TestExplicitDocumentationRiskKeepsBaseline verifies an explicit documentation risk rule keeps required baseline checks.
func TestExplicitDocumentationRiskKeepsBaseline(t *testing.T) {
	p := model.Policy{Checks: []model.Check{{Name: "baseline"}}, RiskRules: []model.RiskRule{{Level: "low", Paths: []string{"docs/**"}}}}
	risk, checks := Select(p, []string{"docs/readme.md"})
	if risk != "low" || len(checks) != 1 {
		t.Fatalf("%s %+v", risk, checks)
	}
}

// TestAPISymbolAndCodeTypeSelectAdditionalChecks verifies exported API changes and code extensions trigger configured extra checks.
func TestAPISymbolAndCodeTypeSelectAdditionalChecks(t *testing.T) {
	p := model.Policy{Checks: []model.Check{{Name: "baseline"}}, RiskRules: []model.RiskRule{{Level: "high", APISymbols: []string{"Authorize*"}, Checks: []model.Check{{Name: "security"}}}, {Level: "critical", CodeTypes: []string{".sql"}}}}
	risk, checks := Select(p, []string{"service.go"}, "+func AuthorizeUser() {}\n")
	if risk != "high" || len(checks) != 2 {
		t.Fatalf("API signal %s %+v", risk, checks)
	}
	risk, _ = Select(p, []string{"migration.sql"})
	if risk != "critical" {
		t.Fatal(risk)
	}
}
