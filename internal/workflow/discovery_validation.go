package workflow

import (
	"errors"
	"sort"
	"strings"
	"whirlwind/internal/model"
)

func validateCandidateEvidence(directions []model.Direction, snapshot discoverySnapshot) error {
	available := map[string]model.EvidenceRef{}
	for _, ref := range snapshot.Evidence {
		available[ref.Kind+":"+ref.Ref] = ref
	}
	for n := range directions {
		if err := validateDirectionEvidence(&directions[n], available, snapshot.Commit); err != nil {
			return err
		}
	}
	return nil
}

func validateDirectionEvidence(candidate *model.Direction, available map[string]model.EvidenceRef, revision string) error {
	if err := validateDirectionAssessment(*candidate); err != nil {
		return err
	}
	if strings.TrimSpace(candidate.Problem) == "" || strings.TrimSpace(candidate.Mechanism) == "" || len(candidate.Components) == 0 {
		return errors.New("candidate must identify a current problem, concrete mechanism, and affected components")
	}
	canonical, directFailure, err := canonicalDirectionEvidence(candidate.Evidence, available)
	if err != nil {
		return err
	}
	if candidate.ProblemKind == "observed_defect" && !directFailure {
		return errors.New("observed_defect requires persisted verification failure evidence; classify unverified source hypotheses as opportunity")
	}
	sort.Slice(canonical, func(i, j int) bool {
		return canonical[i].Kind+":"+canonical[i].Ref < canonical[j].Kind+":"+canonical[j].Ref
	})
	candidate.Evidence = canonical
	if strings.TrimSpace(revision) == "" {
		return errors.New("candidate is not bound to a pinned repository revision")
	}
	candidate.RepositoryRevision = revision
	candidate.Fingerprint = model.ProposalFingerprint(candidate.ProjectID, candidate.Problem, candidate.Mechanism, candidate.Components)
	if candidate.Fingerprint == "" {
		return errors.New("candidate problem fingerprint could not be normalized")
	}
	candidate.Status = "proposed"
	return nil
}

func validateDirectionAssessment(candidate model.Direction) error {
	if err := validateAssessmentFields(candidate); err != nil {
		return err
	}
	return validateAssessmentUncertainty(candidate.Uncertainty)
}

func validateAssessmentFields(candidate model.Direction) error {
	if strings.TrimSpace(candidate.ExpectedBenefit) == "" || strings.TrimSpace(candidate.EffortEstimate) == "" {
		return errors.New("candidate must state expected_benefit and effort_estimate")
	}
	if candidate.ProblemKind != "observed_defect" && candidate.ProblemKind != "opportunity" {
		return errors.New("candidate problem_kind must distinguish observed_defect from opportunity")
	}
	if candidate.ProblemKind == "opportunity" && len(candidate.Uncertainty) == 0 {
		return errors.New("an opportunity must state uncertainty and validation still needed")
	}
	if len(candidate.ExpectedBenefit) > 2000 || len(candidate.EffortEstimate) > 500 || len(candidate.Uncertainty) > 10 {
		return errors.New("candidate assessment exceeds bounded metadata size")
	}
	return nil
}

func validateAssessmentUncertainty(values []string) error {
	for _, uncertainty := range values {
		if strings.TrimSpace(uncertainty) == "" || len(uncertainty) > 1000 {
			return errors.New("candidate uncertainty must be concise nonempty text")
		}
	}
	return nil
}

func canonicalDirectionEvidence(evidence []model.EvidenceRef, available map[string]model.EvidenceRef) ([]model.EvidenceRef, bool, error) {
	if len(evidence) == 0 {
		return nil, false, errors.New("candidate must cite supplied evidence")
	}
	canonical := make([]model.EvidenceRef, 0, len(evidence))
	directFailure := false
	for _, ref := range evidence {
		supplied, ok := available[ref.Kind+":"+ref.Ref]
		if !ok {
			return nil, false, errors.New("candidate cites evidence outside the supplied snapshot")
		}
		canonical = append(canonical, supplied)
		if supplied.Kind == "verification" {
			directFailure = true
		}
	}
	return canonical, directFailure, nil
}
