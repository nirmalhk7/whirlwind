package workflow

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"whirlwind/internal/model"
)

func (step *taskStep) planReady() (bool, error) {
	if _, err := readyPlanBody(step.project, step.task.Plan); err != nil {
		return false, err
	}
	return false, step.WriteLocalPlan(step.ctx, step.project, &step.task)
}

func (step *taskStep) publishReadyPlan() (model.Issue, error) {
	body, err := readyPlanBody(step.project, step.task.Plan)
	if err != nil {
		return model.Issue{}, err
	}
	id, intent, version, x := step.publicationIntent(model.Publication{Kind: "issue", Title: step.task.Issue.Title, Body: body})
	if x != nil {
		return model.Issue{}, x
	}
	var i model.Issue
	if intent.Stage == "complete" && intent.Issue != nil {
		i = *intent.Issue
	} else {
		i, x = step.GitHub.CreateIssue(step.ctx, step.project.Repo, intent.Title, intent.Body, "<!-- whirlwind-plan:"+step.task.ID+" -->")
		if x != nil {
			return model.Issue{}, step.publicationFailure(id, intent, version, x)
		}
		intent.Issue = &i
		intent.URL = i.URL
		intent.Stage = "complete"
		intent.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if x = step.Store.Put(step.ctx, "publications", id, intent, version); x != nil {
			return model.Issue{}, x
		}
	}
	return i, nil
}

func readyPlanBody(project model.Project, plan *model.Plan) (string, error) {
	if plan == nil {
		return "", errors.New("ready plan missing")
	}
	if err := ValidateUnitScopes(project, plan.Units); err != nil {
		return "", err
	}
	return planIssueBody(*plan), nil
}

func planIssueBody(plan model.Plan) string {
	var body strings.Builder
	body.WriteString(RenderPlanDocument(plan))
	body.WriteString("\n")
	fmt.Fprintf(&body, "## Objective\n\n%s\n\n## Specification\n\n%s\n\n## Acceptance criteria\n\n", plan.Objective, plan.Specification)
	for _, criterion := range plan.AcceptanceCriteria {
		fmt.Fprintf(&body, "- [ ] %s\n", criterion)
	}
	body.WriteString("\n## Implementation plan\n")
	for _, unit := range plan.Units {
		fmt.Fprintf(&body, "\n### %s\n\n%s\n\nDependencies: %s\n\nAllowed paths: %s\n", unit.ID, unit.Objective, strings.Join(unit.Dependencies, ", "), strings.Join(unit.AllowedPaths, ", "))
	}
	body.WriteString("\n## Verification and review\n\nRun the project's complete required test suite and full-project Qlty checks and smells. Record base-branch debt and block new or worsened failures and code smells. Review the full diff against every acceptance criterion; repair and repeat checks and review until accepted.\n")
	fmt.Fprintf(&body, "\n## Structured plan\n\n```json\n%s\n```\n", jsonText(plan))
	return body.String()
}
