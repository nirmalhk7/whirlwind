package workflow

import (
	"errors"
	"strings"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (step *taskStep) publishing() (bool, error) {
	step.task.Stage = "submitting"
	return false, nil
}

func (step *taskStep) preparePullRequest() (bool, string, model.Publication, int, error) {
	if err := step.ensurePublishIssue(); err != nil {
		return true, "", model.Publication{}, 0, err
	}
	finished, err := step.publicationReady()
	if finished || err != nil {
		return finished, "", model.Publication{}, 0, err
	}
	request, err := step.pullRequestIntent()
	if err != nil {
		return true, "", model.Publication{}, 0, err
	}
	id, intent, version, err := step.publicationIntent(request)
	return err != nil, id, intent, version, err
}

func (step *taskStep) pullRequestIntent() (model.Publication, error) {
	base, err := step.publicationBase()
	if err != nil {
		return model.Publication{}, err
	}
	proof, err := PublicationEvidence(step.ctx, step.Store, step.task, step.project.PolicyHash)
	if err != nil {
		return model.Publication{}, err
	}
	current, err := step.trustedProject(step.ctx, step.project.ID)
	if err != nil {
		return model.Publication{}, err
	}
	if current.PolicyHash != step.project.PolicyHash {
		return model.Publication{}, errors.New("project policy changed before publication")
	}
	return model.Publication{Evidence: &proof, Kind: "pr", Branch: step.task.Branch, Base: base, Head: step.task.ReviewedCommit, Title: step.task.Submission.Title, Body: step.submissionDescription()}, nil
}

func (step *taskStep) publishPullRequest(id string, intent model.Publication, version int) error {
	return errors.New("remote publication is disabled; accepted work is delivered locally")
}
func (step *taskStep) ensurePublishIssue() error {
	if step.Agentic(step.ctx) || step.project.DefaultBranch == "" {
		return nil
	}
	latest, err := step.GitHub.Issue(step.ctx, step.project.Repo, step.task.Issue.Number)
	if err != nil {
		return err
	}
	unchanged := latest.Title == step.task.Issue.Title && latest.Body == step.task.Issue.Body
	eligible := latest.Eligible() || step.task.EligibilityOverride
	if eligible && unchanged {
		return nil
	}
	if _, err = step.Ingest(step.ctx, step.project, latest); err != nil {
		return err
	}
	return store.ErrConflict
}
func (step *taskStep) publicationReady() (bool, error) {
	reviewed, err := github.Run(step.ctx, step.task.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return true, err
	}
	if reviewed != step.task.ReviewedCommit {
		return true, step.revisitReview()
	}
	if !step.task.ChecksPassed || step.task.Score <= 80 || (step.task.Risk == "critical" && !step.task.Approved) {
		return true, errors.New("publication acceptance gate failed")
	}
	return step.verifyPublicationEvidence()
}

func (step *taskStep) verifyPublicationEvidence() (bool, error) {
	if _, err := PublicationEvidence(step.ctx, step.Store, step.task, step.project.PolicyHash); err != nil {
		return true, step.revisitReview()
	}
	if step.task.Risk == "critical" && !approvalMatches(step.task, step.project.PolicyHash) {
		return true, step.revisitReview()
	}
	return step.verifyPublicationRisk()
}
func (step *taskStep) verifyPublicationRisk() (bool, error) {
	previous := step.task.Risk
	if err := step.verify(step.ctx, step.project, &step.task); err != nil {
		return true, err
	}
	if step.task.Risk != previous {
		return true, step.revisitReview()
	}
	if step.task.Risk == "critical" && !step.task.Approved {
		step.task.Stage = "approval"
		return true, step.Store.Put(step.ctx, "tasks", step.task.ID, step.task, step.record.Version)
	}
	return false, nil
}
func (step *taskStep) revisitReview() error {
	step.task.Stage = "review"
	step.task.Approved = false
	step.task.Approval = nil
	return step.Store.Put(step.ctx, "tasks", step.task.ID, step.task, step.record.Version)
}
func (step *taskStep) publicationBase() (string, error) {
	base, err := github.Run(step.ctx, step.project.Path, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		base, err = github.Run(step.ctx, step.project.Path, "git", "branch", "--show-current")
	}
	if err != nil {
		return "", err
	}
	base = strings.TrimPrefix(base, "origin/")
	if step.project.DefaultBranch != "" {
		base = step.project.DefaultBranch
	}
	return base, nil
}
