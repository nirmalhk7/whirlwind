package workflow

import (
	"errors"
	"fmt"
	"strings"
	"whirlwind/internal/model"
)

// EvaluateAdherence scores the frozen approved list, never a reviewer-provided
// denominator or numeric score. Missing and unsupported passes earn no credit.
func EvaluateAdherence(requirements []model.Requirement, results []model.RequirementResult, expected, actual model.Provenance, gates bool) (model.Adherence, error) {
	if err := adherenceProvenance(expected, actual); err != nil {
		return model.Adherence{}, err
	}
	approved, err := approvedRequirementIDs(requirements)
	if err != nil {
		return model.Adherence{}, err
	}
	judgments, err := requirementJudgments(results, approved)
	if err != nil {
		return model.Adherence{}, err
	}
	report := model.Adherence{Total: len(requirements), Provenance: actual}
	for _, requirement := range requirements {
		result, ok := judgments[requirement.ID]
		if !ok {
			result = model.RequirementResult{ID: requirement.ID, Status: "unverified", Explanation: "Reviewer did not evaluate this approved requirement."}
		}
		if result.Status == "pass" {
			report.Passed++
		}
		report.Results = append(report.Results, result)
	}
	report.Score = 100 * float64(report.Passed) / float64(report.Total)
	report.Accepted = gates && report.Score > 80
	return report, nil
}

func adherenceProvenance(expected, actual model.Provenance) error {
	if expected.PlanVersion < 1 {
		return errors.New("complete approved-plan and candidate verification provenance required")
	}
	for _, value := range []string{expected.Task, expected.Head, expected.Base, expected.PolicyHash, expected.VerificationID} {
		if value == "" {
			return errors.New("complete approved-plan and candidate verification provenance required")
		}
	}
	want := evidenceRevision{expected.Task, expected.Head, expected.Base, expected.PolicyHash, expected.PlanVersion}
	got := evidenceRevision{actual.Task, actual.Head, actual.Base, actual.PolicyHash, actual.PlanVersion}
	if got != want || expected.VerificationID != actual.VerificationID {
		return errors.New("requirement evidence refers to another plan version or candidate snapshot")
	}
	return nil
}

func approvedRequirementIDs(requirements []model.Requirement) (map[string]bool, error) {
	if len(requirements) == 0 {
		return nil, errors.New("approved requirement list must not be empty")
	}
	ids := map[string]bool{}
	for _, requirement := range requirements {
		if strings.TrimSpace(requirement.ID) == "" || strings.TrimSpace(requirement.Outcome) == "" || strings.TrimSpace(requirement.EvidenceMethod) == "" {
			return nil, errors.New("each approved requirement needs a stable ID, concrete outcome, and evidence method")
		}
		if ids[requirement.ID] {
			return nil, fmt.Errorf("duplicate approved requirement ID: %s", requirement.ID)
		}
		ids[requirement.ID] = true
	}
	return ids, nil
}

func requirementJudgments(results []model.RequirementResult, approved map[string]bool) (map[string]model.RequirementResult, error) {
	judgments := map[string]model.RequirementResult{}
	for _, result := range results {
		if _, exists := judgments[result.ID]; exists || !approved[result.ID] {
			return nil, fmt.Errorf("duplicate or unapproved requirement judgment: %s", result.ID)
		}
		if result.Status != "pass" && result.Status != "fail" && result.Status != "unverified" {
			return nil, fmt.Errorf("invalid requirement judgment: %s", result.Status)
		}
		if strings.TrimSpace(result.Explanation) == "" {
			return nil, fmt.Errorf("requirement %s judgment lacks explanation", result.ID)
		}
		if result.Status == "pass" && (!hasReference(result.Code) || !hasReference(result.Evidence)) {
			result.Status = "unverified"
			result.Explanation += " Pass lacks code references or verification evidence."
		}
		judgments[result.ID] = result
	}
	return judgments, nil
}

func hasReference(references []string) bool {
	if len(references) == 0 {
		return false
	}
	for _, reference := range references {
		if strings.TrimSpace(reference) == "" {
			return false
		}
	}
	return true
}
