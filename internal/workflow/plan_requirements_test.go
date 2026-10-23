package workflow

import (
	"encoding/json"
	"strings"
	"testing"
	"whirlwind/internal/model"
)

func TestPlannerRequiresWhatWhyBenefitAndWhere(t *testing.T) {
	plan := model.Plan{Objective: "Fix result", What: "Return useful result", Why: "Caller receives wrong value", Benefit: "Correct output", Where: []string{"result.go", "result_test.go"}, AcceptanceCriteria: []string{"Result is correct"}, Units: []model.Unit{{ID: "main", Objective: "Add regression, fix result", AllowedPaths: []string{"result.go", "result_test.go"}}}}
	b, _ := json.Marshal(plan)
	if _, err := decodePlan(string(b)); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"what", "why", "benefit", "where"} {
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
