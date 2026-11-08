package workflow

import (
	"encoding/json"
	"strings"
	"testing"
	"whirlwind/internal/model"
)

func TestPlannerRequiresWhatWhyBenefitAndWhere(t *testing.T) {
	plan := model.Plan{Objective: "Fix result", What: "Return useful result", Why: "Caller receives wrong value", Benefit: "Correct output", Where: []string{"result.go", "result_test.go"}, Scope: []string{"Change result conversion in result.go."}, Exclusions: []string{"Do not change API response shape."}, Steps: []string{"Add failing result test.", "Fix conversion and rerun focused test."}, RepositoryTestInventory: "None configured: inspected go.mod and CI workflows.", RepositoryBuildInventory: "None configured: inspected go.mod and CI workflows.", AcceptanceCriteria: []string{"Result is correct"}, Requirements: []model.Requirement{{ID: "REQ-RESULT", Outcome: "The caller receives the correct result.", EvidenceMethod: "Run TestResultIsCorrect"}}, Checks: []model.Check{{Name: "result", Command: []string{"go", "test", "./internal/result"}}}, Units: []model.Unit{{ID: "main", Objective: "Add regression, fix result", AllowedPaths: []string{"result.go", "result_test.go"}}}}
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
	body := planIssueBody(plan)
	for _, heading := range []string{"## What", "## Why", "## Benefit", "## Where"} {
		if !strings.Contains(body, heading) {
			t.Errorf("published issue omitted %s", heading)
		}
	}
}

func TestPlannerRequiresDetailedScopeExclusionsAndSteps(t *testing.T) {
	plan := model.Plan{Objective: "Fix result", What: "Return useful result", Why: "Caller receives wrong value", Benefit: "Correct output", Where: []string{"result.go"}, Scope: []string{"Change result conversion."}, Exclusions: []string{"Do not change public API."}, Steps: []string{"Add failing test.", "Fix conversion."}, RepositoryTestInventory: "Configured: go test ./... from go.mod.", RepositoryBuildInventory: "Configured: go build ./... from go.mod.", AcceptanceCriteria: []string{"Result is correct"}, Requirements: []model.Requirement{{ID: "REQ-RESULT", Outcome: "The caller receives the correct result.", EvidenceMethod: "Run TestResultIsCorrect"}}, Checks: []model.Check{{Name: "result", Command: []string{"go", "test", "./internal/result"}}}, RepositoryTestCommands: [][]string{{"go", "test", "./..."}}, RepositoryBuildCommands: [][]string{{"go", "build", "./..."}}, Units: []model.Unit{{ID: "main", Objective: "Add regression, fix result", AllowedPaths: []string{"result.go"}}}}
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
	plan := model.Plan{Objective: "Fix result", What: "Repair conversion", Why: "Output is wrong", Benefit: "Correct output", Where: []string{"result.go"}, Scope: []string{"Change conversion."}, Exclusions: []string{"Keep response schema."}, Steps: []string{"Add a regression."}, RepositoryTestInventory: "None configured: inspected go.mod and CI workflows.", RepositoryBuildInventory: "None configured: inspected go.mod and CI workflows.", Checks: []model.Check{{Name: "result", Command: []string{"go", "test", "./..."}}}, AcceptanceCriteria: []string{"Result is correct."}, Requirements: []model.Requirement{{ID: "REQ-RESULT", Outcome: "The result is correct.", EvidenceMethod: "Run TestResult"}}, Units: []model.Unit{{ID: "main", Objective: "Fix result.", AllowedPaths: []string{"result.go"}}}}
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
	plan := model.Plan{Objective: "Fix result", What: "Return useful result", Why: "Caller receives wrong value", Benefit: "Correct output", Where: []string{"result.go"}, Scope: []string{"Fix conversion."}, Exclusions: []string{"Keep API stable."}, Steps: []string{"Add a regression."}, RepositoryTestInventory: "None configured: inspected go.mod and CI workflows.", RepositoryBuildInventory: "None configured: inspected go.mod and CI workflows.", Checks: []model.Check{{Name: "result", Command: []string{"go", "test", "./..."}}}, AcceptanceCriteria: []string{"Result is correct"}, Requirements: []model.Requirement{{ID: "REQ-RESULT", Outcome: "The result is correct."}}, Units: []model.Unit{{ID: "main", Objective: "Fix result", AllowedPaths: []string{"result.go"}}}}
	b, _ := json.Marshal(plan)
	if _, err := decodePlan(string(b)); err == nil || !strings.Contains(err.Error(), "evidence method") {
		t.Fatalf("planner accepted requirement with no evidence method: %v", err)
	}
}

func TestPlanVerificationChecksIncludeApprovedPlanCommands(t *testing.T) {
	plan := model.Plan{Checks: []model.Check{{Name: "focused", Command: []string{"go", "test", "./internal/result"}}}, RepositoryTestCommands: [][]string{{"go", "test", "./..."}}, RepositoryBuildCommands: [][]string{{"go", "build", "./..."}}}
	checks, err := planVerificationChecks(model.Policy{Checks: []model.Check{{Name: "configured", Command: []string{"go", "test", "./internal/config"}}}}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 6 || checks[0].Name != "configured" || checks[1].Name != "focused" || checks[2].Name != "plan-test-1" || checks[3].Name != "plan-build-1" || checks[4].Name != "qlty-smells" || checks[5].Name != "qlty-check" {
		t.Fatalf("verification omitted or reordered approved checks: %+v", checks)
	}
}

func TestPlanVerificationRejectsGeneratedCheckNameCollisions(t *testing.T) {
	tests := []struct {
		name   string
		plan   model.Plan
		policy model.Policy
	}{
		{
			name:   "configured check collision",
			plan:   model.Plan{RepositoryTestCommands: [][]string{{"go", "test", "./..."}}},
			policy: model.Policy{Checks: []model.Check{{Name: "plan-test-1", Command: []string{"go", "test", "./old"}}}},
		},
		{
			name: "approved check collision",
			plan: model.Plan{
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

func TestPlanVerificationAlwaysIncludesMandatoryQltyGates(t *testing.T) {
	checks, err := planVerificationChecks(model.Policy{}, model.Plan{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"qlty-smells": {"qlty", "smells", "--all", "--sarif"},
		"qlty-check":  {"qlty", "check", "--all", "--sarif"},
	}
	for name, command := range want {
		found := false
		for _, check := range checks {
			if check.Name == name && strings.Join(check.Command, " ") == strings.Join(command, " ") {
				found = true
			}
		}
		if !found {
			t.Errorf("missing mandatory Qlty gate %s: %+v", name, checks)
		}
	}
}
