package workflow

import (
	"errors"
	"time"
	"whirlwind/internal/model"
)

func (step *taskStep) planReady() (bool, error) {
	if step.task.Plan == nil {
		return true, errors.New("ready plan missing")
	}
	body := step.task.Plan.Objective + "\n\nAcceptance criteria:\n" + jsonText(step.task.Plan.AcceptanceCriteria) + "\n\nImplementation units:\n" + jsonText(step.task.Plan.Units)
	id, intent, version, x := step.publicationIntent(model.Publication{Kind: "issue", Title: step.task.Issue.Title, Body: body})
	if x != nil {
		return true, x
	}
	var i model.Issue
	if intent.Stage == "complete" && intent.Issue != nil {
		i = *intent.Issue
	} else {
		i, x = step.GitHub.CreateIssue(step.ctx, step.project.Repo, intent.Title, intent.Body, "<!-- whirlwind-plan:"+step.task.ID+" -->")
		if x != nil {
			return true, step.publicationFailure(id, intent, version, x)
		}
		intent.Issue = &i
		intent.URL = i.URL
		intent.Stage = "complete"
		intent.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if x = step.Store.Put(step.ctx, "publications", id, intent, version); x != nil {
			return true, x
		}
	}
	step.task.Issue = i
	step.task.Stage = "coding"
	return true, step.Store.BindIssue(step.ctx, step.project.Repo, step.task, step.record.Version)
}
