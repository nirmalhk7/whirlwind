package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

type reviewSnapshot struct{ Commit, Prompt string }

func (step *taskStep) review() (bool, error) {
	if err := step.requireTDDEvidence(step.ctx, step.task); err != nil {
		return step.failedReviewVerification(err)
	}
	if err := step.verify(step.ctx, step.project, &step.task); err != nil {
		return step.failedReviewVerification(err)
	}
	review, commit, err := step.performReview()
	if err != nil {
		return true, err
	}
	if err = step.ensureReadOnly(commit); err != nil {
		return true, err
	}
	step.task.ReviewedCommit = commit
	if err = step.saveReview(review); err != nil {
		return true, err
	}
	return false, step.applyReview(review)
}

func (step *taskStep) failedReviewVerification(err error) (bool, error) {
	if errors.Is(err, store.ErrPersistence) {
		return true, err
	}
	step.task.ChecksPassed = false
	step.task.Stage = "repair"
	step.task.Error = err.Error()
	return true, step.Store.Put(step.ctx, "tasks", step.task.ID, step.task, step.record.Version)
}

func (step *taskStep) performReview() (model.Review, string, error) {
	snapshot, err := step.reviewSnapshot()
	if err != nil {
		return model.Review{}, "", err
	}
	review, err := step.primaryReview(snapshot.Prompt)
	if err != nil {
		return review, "", err
	}
	if err = step.supplementReview(snapshot.Prompt, &review); err != nil {
		return review, "", err
	}
	var previous model.Review
	_ = json.Unmarshal([]byte(step.latestReview(step.ctx, fmt.Sprintf("%s:%d", step.task.ID, step.task.PlanVersion))), &previous)
	review.Findings = MergeFindings(previous.Findings, review.Findings)
	return review, snapshot.Commit, nil
}
func (step *taskStep) reviewSnapshot() (reviewSnapshot, error) {
	commit, err := github.Run(step.ctx, step.task.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return reviewSnapshot{}, err
	}
	previous := step.latestReview(step.ctx, fmt.Sprintf("%s:%d", step.task.ID, step.task.PlanVersion))
	base := step.task.Base + "...HEAD"
	intro := ""
	if prior := step.reusableReview(previous, commit); prior != nil {
		base = prior.Head + ".." + commit
		intro = fmt.Sprintf("Repair delta since review %s at %s. Prior review covers the unchanged source. Review changed code and its interactions against every acceptance criterion; resolve or retain every previous finding. Inspect source or the full base diff whenever needed.\n", prior.ReviewID, prior.Head)
	}
	diff, err := github.Run(step.ctx, step.task.Worktree, "git", "diff", base)
	if err != nil {
		return reviewSnapshot{}, err
	}
	evidence, err := step.reviewExecutionEvidence()
	if err != nil {
		return reviewSnapshot{}, err
	}
	prompt, err := agents.Render("review.md", map[string]string{"Plan": jsonText(step.task.Plan), "Previous": previous, "Diff": intro + diff, "Evidence": evidence, "Source": jsonText(step.task.Issue)})
	if err != nil {
		return reviewSnapshot{}, err
	}
	return reviewSnapshot{commit, prompt}, nil
}

func (step *taskStep) reusableReview(raw, head string) *model.Provenance {
	var previous model.Review
	if json.Unmarshal([]byte(raw), &previous) != nil || previous.Provenance == nil {
		return nil
	}
	p := previous.Provenance
	if p.Task != step.task.ID || p.PlanVersion != step.task.PlanVersion || p.Base != step.task.Base || p.PolicyHash != step.project.PolicyHash || p.Head == "" || p.Head == head || p.ReviewID == "" || p.VerificationID == "" {
		return nil
	}
	record, err := step.Store.Get(step.ctx, "verification", p.VerificationID)
	if err != nil {
		return nil
	}
	var evidence struct {
		Stage      string           `json:"stage"`
		Risk       string           `json:"risk"`
		Provenance model.Provenance `json:"provenance"`
	}
	if json.Unmarshal(record.Data, &evidence) != nil || evidence.Stage != "complete" || evidence.Risk != step.task.Risk || evidence.Provenance.Task != p.Task || evidence.Provenance.Head != p.Head || evidence.Provenance.Base != p.Base || evidence.Provenance.PlanVersion != p.PlanVersion || evidence.Provenance.PolicyHash != p.PolicyHash {
		return nil
	}
	if _, err := github.Run(step.ctx, step.task.Worktree, "git", "merge-base", "--is-ancestor", p.Head, head); err != nil {
		return nil
	}
	return p
}
func (step *taskStep) ensureReadOnly(before string) error {
	after, err := github.Run(step.ctx, step.task.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if after != before {
		return errors.New("read-only reviewer changed commit")
	}
	dirty, err := github.Run(step.ctx, step.task.Worktree, "git", "status", "--porcelain", "-uall")
	if err != nil {
		return err
	}
	if dirty != "" {
		return errors.New("read-only reviewer changed working tree")
	}
	return nil
}
func (step *taskStep) saveReview(review model.Review) error {
	step.task.ReviewPasses++
	step.task.Score = review.Score
	key := fmt.Sprintf("%s:%d:%d", step.task.ID, step.task.PlanVersion, step.task.ReviewPasses)
	step.task.ReviewID = key
	record, err := step.Store.Get(step.ctx, "verification", step.task.VerificationID)
	if err != nil {
		return err
	}
	var v struct {
		Provenance model.Provenance `json:"provenance"`
	}
	if err = json.Unmarshal(record.Data, &v); err != nil {
		return err
	}
	attempts := append([]string{}, v.Provenance.AttemptIDs...)
	for _, id := range step.reviewAttempts {
		if !slices.Contains(attempts, id) {
			attempts = append(attempts, id)
		}
	}
	review.Provenance = &model.Provenance{Task: step.task.ID, PlanVersion: step.task.PlanVersion, Head: step.task.ReviewedCommit, Base: step.task.Base, PolicyHash: step.project.PolicyHash, VerificationID: step.task.VerificationID, ReviewID: key, AttemptIDs: attempts}
	step.pending = append(step.pending, store.Change{Kind: "reviews", ID: key, Value: review})
	for _, finding := range review.Findings {
		key := fmt.Sprintf("%s:%d:%s", step.task.ID, step.task.PlanVersion, finding.ID)
		old, err := step.Store.Get(step.ctx, "findings", key)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		step.pending = append(step.pending, store.Change{Kind: "findings", ID: key, Value: finding, Expected: old.Version})
	}
	return nil
}

func (step *taskStep) applyReview(review model.Review) error {
	if Accepted(review, step.task.ChecksPassed) {
		step.task.Handoff.CompletedCriteria = append([]string{}, step.task.Plan.AcceptanceCriteria...)
		if step.task.Submission == nil {
			step.task.Stage = "submitting"
			step.task.Handoff.NextAction = "organize accepted diff and prepare pull request"
			return nil
		}
		step.task.Submission.Stage = "verified"
		if step.task.Risk == "critical" && !step.task.Approved {
			step.task.Stage = "approval"
			step.notify("critical_approval", "Critical change verified and reviewed; approve PR publication")
		} else {
			step.task.Stage = "publishing"
		}
		return nil
	}
	step.task.Stage = "repair"
	step.task.Handoff.NextAction = "correct unresolved review findings"
	return step.progress(step.ctx, &step.task)
}

func (step *taskStep) notify(kind, message string) {
	value := model.Notification{ID: store.ID(), TaskID: step.task.ID, Kind: kind, Message: message}
	step.pending = append(step.pending, store.Change{Kind: "inbox", ID: value.ID, Value: value})
}
