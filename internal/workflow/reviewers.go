package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

func (step *taskStep) primaryReview(prompt, commit string) (model.Review, error) {
	prompt += step.reviewCheckInstructions()
	var review model.Review
	cwd, err := step.reviewerWorktree(commit, "primary")
	if err != nil {
		return review, err
	}
	out, err := step.agent(step.ctx, step.project, step.task, provider.Input{Purpose: "primary", Role: "review", CWD: cwd, Prompt: prompt, ReadOnly: true, SessionID: ""})
	if err != nil {
		return review, err
	}
	if err = provider.JSON(out.Text, &review); err != nil {
		return review, err
	}
	if err = validateReviewFindings(review.Findings); err != nil {
		return review, err
	}
	return review, step.saveReviewPart(review, "primary", out.AttemptID)
}
func validateReviewFindings(findings []model.Finding) error {
	for n := range findings {
		f := &findings[n]
		if f.Message == "" {
			return errors.New("review finding lacks message")
		}
		if f.ID == "" {
			sum := sha256.Sum256([]byte(f.File + ":" + f.Message))
			f.ID = hex.EncodeToString(sum[:8])
		}
	}
	return nil
}
func (step *taskStep) supplementReview(prompt, commit string, review *model.Review) error {
	if step.task.Risk != "high" && step.task.Risk != "critical" {
		return nil
	}
	for _, perspective := range []string{"security", "architecture"} {
		extra, err := step.specialistReview(prompt, perspective, commit)
		if err != nil {
			return err
		}
		review.Summary += "\n" + perspective + ": " + extra.Summary
		review.Findings = MergeFindings(review.Findings, extra.Findings)
		review.Requirements, err = mergeSpecialistRequirements(review.Requirements, extra.Requirements, step.task.Plan)
		if err != nil {
			return fmt.Errorf("%s reviewer returned invalid requirement judgments: %w", perspective, err)
		}
	}
	return nil
}

func mergeSpecialistRequirements(primary, specialist []model.RequirementResult, plan *model.Plan) ([]model.RequirementResult, error) {
	if plan == nil {
		if len(specialist) > 0 {
			return nil, errors.New("specialist judgments require the frozen approved plan")
		}
		return primary, nil
	}
	approved := make(map[string]bool, len(plan.Requirements))
	for _, requirement := range plan.Requirements {
		approved[requirement.ID] = true
	}
	seen := map[string]bool{}
	merged := append([]model.RequirementResult(nil), primary...)
	for _, judgment := range specialist {
		if seen[judgment.ID] {
			return nil, fmt.Errorf("duplicate specialist judgment: %s", judgment.ID)
		}
		seen[judgment.ID] = true
		if !approved[judgment.ID] {
			return nil, fmt.Errorf("unapproved requirement judgment: %s", judgment.ID)
		}
		if judgment.Status != "pass" && judgment.Status != "fail" && judgment.Status != "unverified" {
			return nil, fmt.Errorf("invalid specialist judgment status: %s", judgment.Status)
		}
		index := -1
		for candidate := range merged {
			if merged[candidate].ID == judgment.ID {
				index = candidate
				break
			}
		}
		if index < 0 {
			if judgment.Status == "pass" {
				judgment.Status = "unverified"
				judgment.Explanation = "Specialist pass cannot establish a requirement omitted by the primary review. " + judgment.Explanation
			}
			merged = append(merged, judgment)
			continue
		}
		if judgment.Status == "pass" {
			continue
		}
		current := &merged[index]
		if judgment.Status == "fail" || current.Status != "fail" {
			current.Status = judgment.Status
		}
		current.Explanation += " Specialist finding: " + judgment.Explanation
		current.Code = append(current.Code, judgment.Code...)
		current.Evidence = append(current.Evidence, judgment.Evidence...)
	}
	return merged, nil
}
func (step *taskStep) specialistReview(prompt, perspective, commit string) (model.Review, error) {
	prompt += step.reviewCheckInstructions()
	var extra model.Review
	specialized, err := agents.Render("specialist", map[string]string{"Prompt": prompt, "Perspective": perspective})
	if err != nil {
		return extra, err
	}
	cwd, err := step.reviewerWorktree(commit, perspective)
	if err != nil {
		return extra, err
	}
	out, err := step.agent(step.ctx, step.project, step.task, provider.Input{Purpose: perspective, Role: "review", CWD: cwd, Prompt: specialized, ReadOnly: true, SessionID: ""})
	if err != nil {
		return extra, err
	}
	if err = provider.JSON(out.Text, &extra); err != nil {
		return extra, err
	}
	for n := range extra.Findings {
		f := &extra.Findings[n]
		if f.ID == "" {
			sum := sha256.Sum256([]byte(perspective + f.File + f.Message))
			f.ID = hex.EncodeToString(sum[:8])
		}
	}
	return extra, step.saveReviewPart(extra, perspective, out.AttemptID)
}

func (step *taskStep) reviewCheckInstructions() string {
	checks := append([]model.Check{}, step.project.Policy.Checks...)
	for _, rule := range step.project.Policy.RiskRules {
		checks = append(checks, rule.Checks...)
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, check := range checks {
		if !seen[check.Name] {
			ids = append(ids, check.Name)
			seen[check.Name] = true
		}
	}
	return "\nThe callable run_check tool (whirlwind_checks MCP on ACP/Claude) accepts only {\"check_id\":\"exact ID\"}. It runs committed checks in a disposable snapshot of the reviewed HEAD; source stays read-only. Use it when existing check evidence is insufficient; do not request arbitrary commands or source mutations. Committed check IDs: " + jsonText(ids)
}

func (step *taskStep) saveReviewPart(review model.Review, perspective, attempt string) error {
	review.Perspective = perspective
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
	review.Provenance = &v.Provenance
	review.Provenance.AttemptIDs = append(review.Provenance.AttemptIDs, attempt)
	if err = step.Store.Put(step.ctx, "review_parts", attempt, review, 0); err != nil {
		return fmt.Errorf("%w: %v", store.ErrPersistence, err)
	}
	step.reviewAttempts = append(step.reviewAttempts, attempt)
	return nil
}
