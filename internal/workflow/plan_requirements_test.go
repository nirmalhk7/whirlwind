package workflow

import (
	"encoding/json"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

const (
	requirementFixturePackage     = "./internal/result"
	requirementFixtureWhy         = "Caller receives wrong value"
	requirementFixtureBenefit     = "Correct output"
	requirementFixtureObjective   = "Fix result"
	requirementFixtureNoInventory = "None configured: inspected go.mod and CI workflows."
	requirementFixtureResultID    = "REQ-RESULT"
	requirementFixtureOutcome     = "Result is correct"
	requirementFixtureTitle       = "Return useful result"
	requirementFixtureFile        = "result.go"
)

func TestPlannerRequiresWhatWhyBenefitAndWhere(t *testing.T) {
	plan := model.PlanSpecification{Objective: requirementFixtureObjective, What: requirementFixtureTitle, Why: requirementFixtureWhy, Benefit: requirementFixtureBenefit, Where: []string{requirementFixtureFile, "result_test.go"}, Scope: []string{"Change result conversion in result.go."}, Exclusions: []string{"Do not change API response shape."}, Steps: []string{"Add failing result test.", "Fix conversion and rerun focused test."}, RepositoryTestInventory: requirementFixtureNoInventory, RepositoryBuildInventory: requirementFixtureNoInventory, AcceptanceCriteria: []string{requirementFixtureOutcome}, Requirements: []model.Requirement{{ID: requirementFixtureResultID, Outcome: "The caller receives the correct result.", EvidenceMethod: "Run TestResultIsCorrect"}}, Checks: []model.Check{{Name: "result", Command: []string{"go", "test", requirementFixturePackage}}}, Units: []model.Unit{{ID: "main", Objective: "Add regression, fix result", AllowedPaths: []string{requirementFixtureFile, "result_test.go"}}}}
	b, _ := json.Marshal(plan)
	if _, err := decodePlan(string(b)); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"what", "why", "benefit", "where", "scope", "exclusions", "steps"} {
		var value map[string]any
		_ = json.Unmarshal(b, &value)
		delete(value, field)
		raw, _ := json.Marshal(value)
		if _, err := decodePlan(string(raw)); err == nil {
			t.Errorf("planner omitted %s", field)
		}
	}
	body := RenderPlanDocument(plan)
	for _, heading := range []string{"## What", "## Why", "## Benefit", "## Where"} {
		if !strings.Contains(body, heading) {
			t.Errorf("local plan omitted %s", heading)
		}
	}
}

func TestPlannerRequiresDetailedScopeExclusionsAndSteps(t *testing.T) {
	plan := model.PlanSpecification{Objective: requirementFixtureObjective, What: requirementFixtureTitle, Why: requirementFixtureWhy, Benefit: requirementFixtureBenefit, Where: []string{requirementFixtureFile}, Scope: []string{"Change result conversion."}, Exclusions: []string{"Do not change public API."}, Steps: []string{"Add failing test.", "Fix conversion."}, RepositoryTestInventory: "Configured: go test ./... from go.mod.", RepositoryBuildInventory: "Configured: go build ./... from go.mod.", AcceptanceCriteria: []string{requirementFixtureOutcome}, Requirements: []model.Requirement{{ID: requirementFixtureResultID, Outcome: "The caller receives the correct result.", EvidenceMethod: "Run TestResultIsCorrect"}}, Checks: []model.Check{{Name: "result", Command: []string{"go", "test", requirementFixturePackage}}}, RepositoryTestCommands: [][]string{{"go", "test", "./..."}}, RepositoryBuildCommands: [][]string{{"go", "build", "./..."}}, Units: []model.Unit{{ID: "main", Objective: "Add regression, fix result", AllowedPaths: []string{requirementFixtureFile}}}}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePlan(string(encoded)); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"scope", "exclusions", "steps"} {
		var value map[string]any
		if err := json.Unmarshal(encoded, &value); err != nil {
			t.Fatal(err)
		}
		delete(value, field)
		missing, _ := json.Marshal(value)
		if _, err := decodePlan(string(missing)); err == nil {
			t.Errorf("planner accepted missing %s", field)
		}
	}
}

func TestPlannerInventoryMustMatchConfiguredCommandLists(t *testing.T) {
	plan := model.PlanSpecification{Objective: requirementFixtureObjective, What: "Repair conversion", Why: "Output is wrong", Benefit: requirementFixtureBenefit, Where: []string{requirementFixtureFile}, Scope: []string{"Change conversion."}, Exclusions: []string{"Keep response schema."}, Steps: []string{"Add a regression."}, RepositoryTestInventory: requirementFixtureNoInventory, RepositoryBuildInventory: requirementFixtureNoInventory, Checks: []model.Check{{Name: "result", Command: []string{"go", "test", "./..."}}}, AcceptanceCriteria: []string{"Result is correct."}, Requirements: []model.Requirement{{ID: requirementFixtureResultID, Outcome: "The result is correct.", EvidenceMethod: "Run TestResult"}}, Units: []model.Unit{{ID: "main", Objective: "Fix result.", AllowedPaths: []string{requirementFixtureFile}}}}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePlan(string(encoded)); err != nil {
		t.Fatalf("honest absence inventory rejected: %v", err)
	}
	plan.RepositoryTestCommands = [][]string{{"go", "test", "./..."}}
	encoded, _ = json.Marshal(plan)
	if _, err := decodePlan(string(encoded)); err == nil || !strings.Contains(err.Error(), "repository test command inventory") {
		t.Fatalf("configured tests with a false absence statement accepted: %v", err)
	}
}

func TestPlannerRejectsRequirementWithoutEvidenceMethod(t *testing.T) {
	plan := model.PlanSpecification{Objective: requirementFixtureObjective, What: requirementFixtureTitle, Why: requirementFixtureWhy, Benefit: requirementFixtureBenefit, Where: []string{requirementFixtureFile}, Scope: []string{"Fix conversion."}, Exclusions: []string{"Keep API stable."}, Steps: []string{"Add a regression."}, RepositoryTestInventory: requirementFixtureNoInventory, RepositoryBuildInventory: requirementFixtureNoInventory, Checks: []model.Check{{Name: "result", Command: []string{"go", "test", "./..."}}}, AcceptanceCriteria: []string{requirementFixtureOutcome}, Requirements: []model.Requirement{{ID: requirementFixtureResultID, Outcome: "The result is correct."}}, Units: []model.Unit{{ID: "main", Objective: requirementFixtureObjective, AllowedPaths: []string{requirementFixtureFile}}}}
	b, _ := json.Marshal(plan)
	if _, err := decodePlan(string(b)); err == nil || !strings.Contains(err.Error(), "evidence method") {
		t.Fatalf("planner accepted requirement with no evidence method: %v", err)
	}
}

func TestPlanVerificationChecksIncludeApprovedPlanCommands(t *testing.T) {
	plan := model.PlanSpecification{Checks: []model.Check{{Name: "focused", Command: []string{"go", "test", requirementFixturePackage}}}, RepositoryTestCommands: [][]string{{"go", "test", "./..."}}, RepositoryBuildCommands: [][]string{{"go", "build", "./..."}}}
	checks, err := planVerificationChecks(model.Policy{Checks: []model.Check{{Name: "configured", Command: []string{"go", "test", "./internal/config"}}}}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 4 || checks[0].Name != "configured" || checks[1].Name != "focused" || checks[2].Name != "plan-test-1" || checks[3].Name != "plan-build-1" {
		t.Fatalf("verification omitted or reordered approved checks: %+v", checks)
	}
}

func TestPlanVerificationRejectsGeneratedCheckNameCollisions(t *testing.T) {
	tests := []struct {
		name   string
		plan   model.PlanSpecification
		policy model.Policy
	}{
		{
			name:   "configured check collision",
			plan:   model.PlanSpecification{RepositoryTestCommands: [][]string{{"go", "test", "./..."}}},
			policy: model.Policy{Checks: []model.Check{{Name: "plan-test-1", Command: []string{"go", "test", "./old"}}}},
		},
		{
			name: "approved check collision",
			plan: model.PlanSpecification{
				Checks:                  []model.Check{{Name: "plan-build-1", Command: []string{"go", "build", "./old"}}},
				RepositoryBuildCommands: [][]string{{"go", "build", "./..."}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := planVerificationChecks(test.policy, test.plan); err == nil {
				t.Fatal("accepted colliding verification check name")
			}
		})
	}
}

func TestParityQltyCommandsRequireExplicitConfiguration(t *testing.T) {
	checks, err := planVerificationChecks(model.Policy{}, model.PlanSpecification{})
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 0 {
		t.Fatalf("unapproved commands injected: %+v", checks)
	}
	configured := model.Check{Name: "qlty-check", Command: []string{"qlty", "check", "src/new.go", "--sarif"}, TimeoutSeconds: 30}
	checks, err = planVerificationChecks(model.Policy{Checks: []model.Check{configured}}, model.PlanSpecification{})
	if err != nil || len(checks) != 1 || strings.Join(checks[0].Command, "\x00") != strings.Join(configured.Command, "\x00") {
		t.Fatalf("explicit scoped Qlty command replaced: %+v %v", checks, err)
	}
}
