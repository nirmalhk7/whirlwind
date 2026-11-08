package store

import (
	"fmt"
	"time"
	"whirlwind/internal/model"
)

func updateIssueTask(t *model.Task, i model.Issue) bool {
	if t.Cancelled() || issueUpdateIsStale(t.Issue, i) {
		return false
	}
	if !i.Eligible() {
		return withdrawIssueTask(t, i)
	}
	if resumeWithdrawnIssue(t, i) {
		return true
	}
	if sameIssueContent(t.Issue, i) {
		return updateIssueTimestamp(t, i)
	}
	invalidateIssueTask(t)
	t.Issue = i
	return true
}

func issueUpdateIsStale(previous, incoming model.Issue) bool {
	incomingTime, incomingErr := time.Parse(time.RFC3339, incoming.UpdatedAt)
	existingTime, existingErr := time.Parse(time.RFC3339, previous.UpdatedAt)
	return incomingErr == nil && existingErr == nil && incomingTime.Before(existingTime)
}

func resumeWithdrawnIssue(task *model.Task, issue model.Issue) bool {
	withdrawn := !task.Issue.Eligible() && task.Stage == "paused" && task.Error == "Issue closed or whirlwind:ready removed"
	if !withdrawn || !sameIssueContent(task.Issue, issue) {
		return false
	}
	task.Stage = task.ResumeStage
	if task.Stage == "" {
		task.Stage = "planning"
	}
	task.Issue = issue
	task.ResumeStage = ""
	task.Error = ""
	task.EligibilityOverride = false
	return true
}

func updateIssueTimestamp(task *model.Task, issue model.Issue) bool {
	if task.Issue.UpdatedAt == issue.UpdatedAt {
		return false
	}
	task.Issue = issue
	return true
}

func withdrawIssueTask(t *model.Task, i model.Issue) bool {
	if t.EligibilityOverride && sameIssueContent(t.Issue, i) {
		return false
	}
	if t.Stage == "paused" {
		return false
	}
	t.ResumeStage = t.Stage
	t.Stage = "paused"
	t.Error = "Issue closed or whirlwind:ready removed"
	t.Issue = i
	return true
}

func sameIssueContent(a, b model.Issue) bool {
	return a.Body == b.Body && a.Title == b.Title
}

func invalidateIssueTask(t *model.Task) {
	t.ImplementationAttempts = nil
	t.Approval = nil
	t.VerificationID = ""
	t.ReviewID = ""
	t.LastAttemptID = ""
	if t.PR != "" {
		t.Error = "Issue changed after PR creation; review published PR in GitHub"
		t.ResumeStage = t.Stage
		t.Stage = "paused"
	} else {
		t.PlanVersion++
		t.ResumeStage = ""
		t.EligibilityOverride = false
		t.ReviewedCommit = ""
		t.Plan = nil
		t.Worktree = ""
		t.Branch = ""
		t.Base = ""
		t.ReviewPasses = 0
		t.Recoveries = 0
		t.UnchangedFailures = 0
		t.Fingerprint = ""
		t.Stage = "awaiting_selection"
		t.Selection = nil
		t.ChecksPassed = false
		t.Approved = false
		t.Score = 0
		t.Handoff = model.Handoff{Version: 1, NextAction: "select updated issue before replanning"}
	}
}

func authorizeReady(t *model.Task, p model.Project) {
	if t.Issue.Number > 0 {
		t.Sources = []model.SourceRef{{Host: "github.com", Connection: "gh", Repository: p.Repo, ID: fmt.Sprint(t.Issue.Number), URL: t.Issue.URL, Revision: t.Issue.UpdatedAt}}
	}
	t.Selection = &model.SelectionAuthorization{CandidateID: "ready-issue-" + t.ID, CandidateVersion: t.PlanVersion, Objective: t.Issue.Title, Scope: []string{t.Issue.Body}, PolicyHash: p.PolicyHash, SourceFingerprint: model.IssueFingerprint(t.Issue), SelectedAt: time.Now().UTC().Format(time.RFC3339Nano)}
}
