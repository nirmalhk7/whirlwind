package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/prompts/agents"
)

func (step *taskStep) submitting() (bool, error) {
	if step.task.ReviewedCommit == "" || !step.task.ChecksPassed || step.task.Score < 9 {
		return true, errors.New("submission requires accepted review and verification")
	}
	if step.task.Submission == nil {
		return step.prepareSubmission()
	}
	return step.organizePreparedSubmission()
}

type submissionPlan struct {
	Title, Body string
	Commits     []CommitGroup
}

func (step *taskStep) submissionPlanOutput() (provider.Output, error) {
	diff, err := github.Run(step.ctx, step.task.Worktree, "git", "diff", "--binary", step.task.Base+"...HEAD")
	if err != nil {
		return provider.Output{}, err
	}
	prompt, err := agents.Render("submit.md", map[string]string{"Plan": jsonText(step.task.Plan), "Sources": jsonText(step.task.Sources), "Diff": diff, "Validation": step.prDescription()})
	if err != nil {
		return provider.Output{}, err
	}
	return step.agent(step.ctx, step.project, step.task, provider.Input{Role: "submit", Purpose: "submission_plan", CWD: step.task.Worktree, Prompt: prompt, ReadOnly: true})
}

func decodeSubmissionPlan(text string) (submissionPlan, error) {
	var result submissionPlan
	if err := provider.JSON(text, &result); err != nil {
		return result, err
	}
	if strings.TrimSpace(result.Title) == "" || strings.TrimSpace(result.Body) == "" || len(result.Commits) == 0 {
		return result, errors.New("submitter omitted title, human-readable body or logical commits")
	}
	return result, nil
}

func (step *taskStep) prepareSubmission() (bool, error) {
	out, err := step.submissionPlanOutput()
	if err != nil {
		return true, err
	}
	result, err := decodeSubmissionPlan(out.Text)
	if err != nil {
		return true, err
	}
	groups, _ := json.Marshal(result.Commits)
	step.task.Submission = &model.Submission{Stage: "prepared", OriginalWorktree: step.task.Worktree, OriginalBranch: step.task.Branch, ReviewedCommit: step.task.ReviewedCommit, Title: result.Title, Body: result.Body, Groups: groups}
	step.task.LastAttemptID = out.AttemptID
	return false, nil
}

func (step *taskStep) organizePreparedSubmission() (bool, error) {
	submission := step.task.Submission
	var groups []CommitGroup
	if err := json.Unmarshal(submission.Groups, &groups); err != nil {
		return true, err
	}
	suffix := submission.ReviewedCommit
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	target := filepath.Join(filepath.Dir(submission.OriginalWorktree), "submission-"+suffix)
	branch := submission.OriginalBranch + "-submit-" + suffix
	if _, err := organizeSubmission(step.ctx, submission.OriginalWorktree, target, branch, step.task.Base, submission.ReviewedCommit, groups); err != nil {
		return true, fmt.Errorf("organize logical commits: %w", err)
	}
	submission.Stage = "organized"
	step.task.Worktree = target
	step.task.Branch = branch
	step.task.Stage = "review"
	step.task.ReviewedCommit = ""
	step.task.VerificationID = ""
	step.task.ReviewID = ""
	step.task.Approved = false
	step.task.Approval = nil
	step.task.Handoff.NextAction = "verify final commits and confirm reviewer acceptance"
	return false, nil
}
