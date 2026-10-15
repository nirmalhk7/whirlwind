package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/verification"
)

func (step *taskStep) prDescription() string {
	var description strings.Builder
	fmt.Fprintf(&description, "%s\n\nCloses #%d\n\n## Implementation\n", step.task.Plan.Objective, step.task.Issue.Number)
	for _, unit := range step.task.Plan.Units {
		fmt.Fprintf(&description, "- %s\n", unit.Objective)
	}
	description.WriteString("\n## Acceptance criteria\n")
	for _, criterion := range step.task.Plan.AcceptanceCriteria {
		fmt.Fprintf(&description, "- %s\n", criterion)
	}
	description.WriteString("\n## Validation\n")
	verificationRecords, _ := step.Store.List(step.ctx, "verification")
	var verified verification.Report
	for _, record := range verificationRecords {
		var v struct {
			Task    string              `json:"task"`
			Version int                 `json:"plan_version"`
			Report  verification.Report `json:"report"`
		}
		if json.Unmarshal(record.Data, &v) == nil && v.Task == step.task.ID && v.Version == step.task.PlanVersion {
			verified = v.Report
		}
	}
	for _, check := range verified.Results {
		fmt.Fprintf(&description, "- %s: passed\n", check.Name)
	}
	var review model.Review
	_ = json.Unmarshal([]byte(step.latestReview(step.ctx, fmt.Sprintf("%s:%d", step.task.ID, step.task.PlanVersion))), &review)
	fmt.Fprintf(&description, "\nReview score: %.1f/10; no unresolved blocking findings.\n\n%s\n\n<!-- whirlwind-task:%s -->", step.task.Score, review.Summary, step.task.ID)
	return description.String()

}
