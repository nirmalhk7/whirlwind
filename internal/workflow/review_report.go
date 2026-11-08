package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

type acceptedReviewEvidence struct {
	task         model.Task
	frozen       model.Plan
	review       model.Review
	project      model.Project
	verification verification.Report
	provenance   model.Provenance
}

// LoadAcceptedReviewReport returns a report recomputed from the immutable plan,
// review judgments, and verification artifact. accepted is false for unmet gates
// or unresolved blockers; stale or mismatched provenance returns an error.
func (e *Engine) LoadAcceptedReviewReport(ctx context.Context, taskID string) (model.Review, model.Adherence, bool, error) {
	evidence, err := e.loadAcceptedReviewEvidence(ctx, taskID)
	if err != nil {
		return model.Review{}, model.Adherence{}, false, err
	}
	return e.acceptedReviewReport(ctx, evidence)
}

func (e *Engine) acceptedReviewReport(ctx context.Context, evidence acceptedReviewEvidence) (model.Review, model.Adherence, bool, error) {
	checks, err := planVerificationChecks(evidence.project.Policy, evidence.frozen)
	if err != nil {
		return model.Review{}, model.Adherence{}, false, fmt.Errorf("approved verification inventory is invalid: %w", err)
	}
	checksPassed, err := e.approvedChecksPassed(ctx, evidence.task, evidence.provenance, checks, evidence.verification)
	if err != nil {
		return model.Review{}, model.Adherence{}, false, err
	}
	if !evidence.verification.Passed || !checksPassed {
		return model.Review{}, model.Adherence{}, false, errors.New("verification evidence does not prove every approved check passed")
	}
	report, err := EvaluateAdherence(evidence.frozen.Requirements, evidence.review.Requirements, *evidence.review.Provenance, evidence.provenance, checksPassed)
	if err != nil {
		return model.Review{}, model.Adherence{}, false, err
	}
	evidence.review.Adherence = &report
	return evidence.review, report, Accepted(evidence.review, checksPassed), nil
}

func (e *Engine) loadAcceptedReviewEvidence(ctx context.Context, taskID string) (acceptedReviewEvidence, error) {
	task, frozen, err := e.loadApprovedTaskPlan(ctx, taskID)
	if err != nil {
		return acceptedReviewEvidence{}, err
	}
	project, review, err := e.loadReviewAndProject(ctx, task)
	if err != nil {
		return acceptedReviewEvidence{}, err
	}
	report, provenance, err := e.loadVerificationEvidence(ctx, task, project)
	if err != nil {
		return acceptedReviewEvidence{}, err
	}
	return acceptedReviewEvidence{task: task, frozen: frozen, review: review, project: project, verification: report, provenance: provenance}, nil
}

func (e *Engine) loadApprovedTaskPlan(ctx context.Context, taskID string) (model.Task, model.Plan, error) {
	task, err := e.loadReviewTask(ctx, taskID)
	if err != nil {
		return model.Task{}, model.Plan{}, err
	}
	frozen, err := e.loadFrozenPlan(ctx, task)
	return task, frozen, err
}

func (e *Engine) loadReviewTask(ctx context.Context, taskID string) (model.Task, error) {
	record, err := e.Store.Get(ctx, "tasks", taskID)
	if err != nil {
		return model.Task{}, err
	}
	task, err := store.Decode[model.Task](record)
	if err != nil {
		return model.Task{}, err
	}
	if !taskHasApprovedReview(task) {
		return model.Task{}, errors.New("task lacks an approved plan and completed review evidence")
	}
	return task, nil
}

func taskHasApprovedReview(task model.Task) bool {
	hasTaskIdentity := task.ID != "" && task.ProjectID != ""
	hasPlan := task.PlanVersion > 0 && task.Plan != nil && task.PlanDocument != nil && task.PlanDocument.ApprovedHash != ""
	hasReview := task.ReviewID != "" && task.VerificationID != "" && task.ReviewedCommit != ""
	return hasTaskIdentity && hasPlan && hasReview
}

func (e *Engine) loadFrozenPlan(ctx context.Context, task model.Task) (model.Plan, error) {
	planRecord, err := e.Store.Get(ctx, "plans", fmt.Sprintf("%s:%d", task.ID, task.PlanVersion))
	if err != nil {
		return model.Plan{}, err
	}
	frozen, err := store.Decode[model.Plan](planRecord)
	if err != nil {
		return model.Plan{}, err
	}
	if !sameApprovedPlanSpecification(frozen, *task.Plan) {
		return model.Plan{}, errors.New("task plan differs from its immutable approved snapshot")
	}
	return frozen, nil
}

func (e *Engine) loadReviewAndProject(ctx context.Context, task model.Task) (model.Project, model.Review, error) {
	review, err := e.loadReview(ctx, task)
	if err != nil {
		return model.Project{}, model.Review{}, err
	}
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		return model.Project{}, model.Review{}, err
	}
	if err := validateReviewPolicy(review, project); err != nil {
		return model.Project{}, model.Review{}, err
	}
	return project, review, nil
}

func (e *Engine) loadReview(ctx context.Context, task model.Task) (model.Review, error) {
	record, err := e.Store.Get(ctx, "reviews", task.ReviewID)
	if err != nil {
		return model.Review{}, err
	}
	review, err := store.Decode[model.Review](record)
	if err != nil {
		return model.Review{}, err
	}
	if !reviewMatchesTask(review, task) {
		return model.Review{}, errors.New("review provenance does not match the approved plan and candidate")
	}
	return review, nil
}

func validateReviewPolicy(review model.Review, project model.Project) error {
	if review.Provenance.PolicyHash != project.PolicyHash {
		return errors.New("review policy provenance is stale")
	}
	return nil
}

func reviewMatchesTask(review model.Review, task model.Task) bool {
	provenance := review.Provenance
	return provenance != nil && reviewIdentityMatches(provenance, task) && reviewCandidateMatches(provenance, task)
}

func reviewIdentityMatches(provenance *model.Provenance, task model.Task) bool {
	return provenance.ReviewID == task.ReviewID && provenance.VerificationID == task.VerificationID && provenance.PlanVersion == task.PlanVersion
}

func reviewCandidateMatches(provenance *model.Provenance, task model.Task) bool {
	return provenance.Head == task.ReviewedCommit && provenance.Task == task.ID && provenance.Base == task.Base
}

func (e *Engine) loadVerificationEvidence(ctx context.Context, task model.Task, project model.Project) (verification.Report, model.Provenance, error) {
	record, err := e.Store.Get(ctx, "verification", task.VerificationID)
	if err != nil {
		return verification.Report{}, model.Provenance{}, err
	}
	var evidence struct {
		Stage      string              `json:"stage"`
		Report     verification.Report `json:"report"`
		Provenance model.Provenance    `json:"provenance"`
	}
	if err := json.Unmarshal(record.Data, &evidence); err != nil {
		return verification.Report{}, model.Provenance{}, err
	}
	if !verificationMatchesTask(evidence.Stage, evidence.Provenance, task, project.PolicyHash) {
		return verification.Report{}, model.Provenance{}, errors.New("verification evidence does not match the reviewed candidate")
	}
	if err := e.validateQltyEvidence(ctx, task, evidence.Report); err != nil {
		return verification.Report{}, model.Provenance{}, err
	}
	return evidence.Report, evidence.Provenance, nil
}

func verificationMatchesTask(stage string, provenance model.Provenance, task model.Task, policyHash string) bool {
	return stage == "complete" && verificationIdentityMatches(provenance, task) && verificationCandidateMatches(provenance, task, policyHash)
}

func verificationIdentityMatches(provenance model.Provenance, task model.Task) bool {
	return provenance.Task == task.ID && provenance.PlanVersion == task.PlanVersion && provenance.VerificationID == task.VerificationID
}

func verificationCandidateMatches(provenance model.Provenance, task model.Task, policyHash string) bool {
	return provenance.Head == task.ReviewedCommit && provenance.Base == task.Base && provenance.PolicyHash == policyHash
}

func (e *Engine) approvedChecksPassed(ctx context.Context, task model.Task, provenance model.Provenance, required []model.Check, report verification.Report) (bool, error) {
	results := make(map[string][]verification.Result, len(report.Results))
	passed := true
	for _, result := range report.Results {
		if result.Name == "" || len(result.Command) == 0 {
			return false, errors.New("verification evidence contains an unnamed or commandless check result")
		}
		results[result.Name] = append(results[result.Name], result)
		if !approvedCheckResultPassed(result) {
			passed = false
		}
		check := model.Check{Name: result.Name, Command: result.Command}
		if err := e.validateApprovedCheckRuntime(ctx, runtimeProofInput{task: task, provenance: provenance, check: check, result: result}); err != nil {
			return false, err
		}
	}
	for _, check := range required {
		matches := results[check.Name]
		if len(matches) != 1 {
			return false, fmt.Errorf("verification evidence must contain exactly one result for approved check %q", check.Name)
		}
		if !reflect.DeepEqual(matches[0].Command, check.Command) {
			return false, fmt.Errorf("verification command for approved check %q does not match the frozen plan and current policy", check.Name)
		}
	}
	return passed, nil
}

func approvedCheckResultPassed(result verification.Result) bool {
	if !result.Passed || result.ExitCode == nil {
		return false
	}
	if len(result.Command) > 1 && filepath.Base(result.Command[0]) == "qlty" && (result.Command[1] == "check" || result.Command[1] == "smells") {
		if *result.ExitCode == 0 {
			return result.Error == "" || verification.AcceptedQltyBaselineAdvisory(result)
		}
		return verification.AcceptedQltyBaselineAdvisory(result)
	}
	return *result.ExitCode == 0 && result.Error == ""
}

func sameApprovedPlanSpecification(frozen, current model.Plan) bool {
	frozen = clearUnitRuntime(frozen)
	current = clearUnitRuntime(current)
	return reflect.DeepEqual(frozen, current)
}

func clearUnitRuntime(plan model.Plan) model.Plan {
	plan.Units = append([]model.Unit(nil), plan.Units...)
	for index := range plan.Units {
		unit := &plan.Units[index]
		unit.AttemptID = ""
		unit.Base = ""
		unit.Stage = ""
		unit.Worktree = ""
		unit.Branch = ""
		unit.Commit = ""
	}
	return plan
}
