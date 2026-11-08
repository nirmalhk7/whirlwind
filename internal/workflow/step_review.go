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
	if err := step.verifyReviewReadiness(); err != nil {
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

func (step *taskStep) verifyReviewReadiness() error {
	if err := step.requireTDDEvidence(step.ctx, step.task); err != nil {
		return err
	}
	return step.verify(step.ctx, step.project, &step.task)
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
	review, err := step.primaryReview(snapshot.Prompt, snapshot.Commit)
	if err != nil {
		return review, "", err
	}
	if err = step.supplementReview(snapshot.Prompt, snapshot.Commit, &review); err != nil {
		return review, "", err
	}
	if step.task.Plan == nil {
		return model.Review{}, "", errors.New("review requires the frozen approved plan")
	}
	provenance := model.Provenance{Task: step.task.ID, PlanVersion: step.task.PlanVersion, Head: snapshot.Commit, Base: step.task.Base, PolicyHash: step.project.PolicyHash, VerificationID: step.task.VerificationID}
	report, err := EvaluateAdherence(step.task.Plan.Requirements, review.Requirements, provenance, provenance, step.task.ChecksPassed)
	if err != nil {
		return model.Review{}, "", err
	}
	review.Adherence = &report
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
	prompt, err := agents.Render("review.md", map[string]string{"Plan": jsonText(step.task.Plan), "Requirements": jsonText(step.task.Plan.Requirements), "Previous": previous, "Diff": intro + diff, "Evidence": evidence, "Source": jsonText(step.task.Issue)})
	if err != nil {
		return reviewSnapshot{}, err
	}
	return reviewSnapshot{commit, prompt}, nil
}

func (step *taskStep) reusableReview(raw, head string) *model.Provenance {
	p, ok := step.reviewEvidence(raw, head)
	if !ok {
		return nil
	}
	if _, err := github.Run(step.ctx, step.task.Worktree, "git", "merge-base", "--is-ancestor", p.Head, head); err != nil {
		return nil
	}
	return p
}

func (step *taskStep) reviewEvidence(raw, head string) (*model.Provenance, bool) {
	var previous model.Review
	if !decodeReviewProvenance(raw, &previous) {
		return nil, false
	}
	p := previous.Provenance
	if !reviewProvenanceMatches(p, step.task, step.project, head) {
		return nil, false
	}
	record, err := step.Store.Get(step.ctx, "verification", p.VerificationID)
	if err != nil {
		return nil, false
	}
	var evidence struct {
		Stage      string           `json:"stage"`
		Risk       string           `json:"risk"`
		Provenance model.Provenance `json:"provenance"`
	}
	if !reviewEvidenceMatches(record.Data, evidence, p, step.task.Risk) {
		return nil, false
	}
	return p, true
}

func decodeReviewProvenance(raw string, review *model.Review) bool {
	return json.Unmarshal([]byte(raw), review) == nil && review.Provenance != nil
}

func reviewProvenanceMatches(provenance *model.Provenance, task model.Task, project model.Project, head string) bool {
	return provenanceMatchesTask(provenance, task) && provenanceMatchesProject(provenance, project) && provenanceMatchesHead(provenance, head)
}

func provenanceMatchesTask(provenance *model.Provenance, task model.Task) bool {
	return provenance.Task == task.ID && provenance.PlanVersion == task.PlanVersion && provenance.Base == task.Base
}

func provenanceMatchesProject(provenance *model.Provenance, project model.Project) bool {
	return provenance.PolicyHash == project.PolicyHash
}

func provenanceMatchesHead(provenance *model.Provenance, head string) bool {
	return provenance.Head != "" && provenance.Head != head && provenance.ReviewID != "" && provenance.VerificationID != ""
}

func reviewEvidenceMatches(raw []byte, evidence struct {
	Stage      string           `json:"stage"`
	Risk       string           `json:"risk"`
	Provenance model.Provenance `json:"provenance"`
}, provenance *model.Provenance, risk string) bool {
	if json.Unmarshal(raw, &evidence) != nil || evidence.Stage != "complete" || evidence.Risk != risk {
		return false
	}
	return sameEvidenceTask(evidence.Provenance, provenance) && sameEvidenceReview(evidence.Provenance, provenance)
}

func sameEvidenceTask(actual model.Provenance, expected *model.Provenance) bool {
	return actual.Task == expected.Task && actual.PlanVersion == expected.PlanVersion && actual.Base == expected.Base
}

func sameEvidenceReview(actual model.Provenance, expected *model.Provenance) bool {
	return actual.Head == expected.Head && actual.PolicyHash == expected.PolicyHash
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
	if review.Adherence == nil || review.Adherence.Total == 0 {
		return errors.New("review is missing computed approved-requirement adherence")
	}
	step.task.ReviewPasses++
	step.task.Score = review.Adherence.Score
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
	review.Adherence.Provenance = *review.Provenance
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
		step.task.Handoff.CompletedCriteria = completedRequirementOutcomes(step.task.Plan.Requirements, review.Adherence.Results)
		if step.task.Risk == "critical" && !step.task.Approved {
			step.task.Stage = "approval"
			step.task.Handoff.NextAction = "review and approve the exact critical candidate for local finalization"
			step.notify("critical_approval", criticalApprovalMessage(step.task, step.project.PolicyHash))
			return nil
		}
		if step.task.Submission == nil {
			step.task.Stage = "submitting"
			step.task.Handoff.NextAction = "stage accepted work for local finalization"
			return nil
		}
		step.task.Submission.Stage = "verified"
		step.task.Stage = "publishing"
		return nil
	}
	step.task.Stage = "repair"
	step.task.Handoff.NextAction = "correct unresolved review findings"
	return step.progress(step.ctx, &step.task)
}

func criticalApprovalMessage(task model.Task, policyHash string) string {
	return fmt.Sprintf("Approve local finalization only after reviewing candidate %s, plan version %d, review %s, verification %s, and policy %s.", task.ReviewedCommit, task.PlanVersion, task.ReviewID, task.VerificationID, policyHash)
}

func completedRequirementOutcomes(requirements []model.Requirement, results []model.RequirementResult) []string {
	passed := make(map[string]bool, len(results))
	for _, result := range results {
		if result.Status == "pass" {
			passed[result.ID] = true
		}
	}
	completed := make([]string, 0, len(passed))
	for _, requirement := range requirements {
		if passed[requirement.ID] {
			completed = append(completed, requirement.ID+": "+requirement.Outcome)
		}
	}
	return completed
}

func (step *taskStep) notify(kind, message string) {
	value := model.Notification{ID: store.ID(), TaskID: step.task.ID, Kind: kind, Message: message}
	step.pending = append(step.pending, store.Change{Kind: "inbox", ID: value.ID, Value: value})
}
