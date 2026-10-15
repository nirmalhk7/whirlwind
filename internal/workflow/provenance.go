package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func PublicationEvidence(ctx context.Context, s *store.Store, t model.Task, policyHash string) (model.Provenance, error) {
	if t.ReviewID == "" || t.VerificationID == "" {
		return model.Provenance{}, errors.New("linked verification and review evidence required")
	}
	proof, err := reviewedPublicationEvidence(ctx, s, t, policyHash)
	if err != nil {
		return model.Provenance{}, err
	}
	if err = validatePublicationVerification(ctx, s, t, policyHash, proof.VerificationID); err != nil {
		return model.Provenance{}, err
	}
	return proof, nil
}

func reviewedPublicationEvidence(ctx context.Context, s *store.Store, t model.Task, policyHash string) (model.Provenance, error) {
	record, err := s.Get(ctx, "reviews", t.ReviewID)
	if err != nil {
		return model.Provenance{}, err
	}
	review, err := store.Decode[model.Review](record)
	if err != nil {
		return model.Provenance{}, err
	}
	if review.Provenance == nil || !sameEvidence(*review.Provenance, t, policyHash) || !Accepted(review, true) {
		return model.Provenance{}, errors.New("review evidence is stale or not accepted")
	}
	return *review.Provenance, nil
}

func validatePublicationVerification(ctx context.Context, s *store.Store, t model.Task, policyHash, checkID string) error {
	record, err := s.Get(ctx, "verification", checkID)
	if err != nil {
		return err
	}
	var checks struct {
		Stage      string              `json:"stage"`
		Provenance model.Provenance    `json:"provenance"`
		Report     verification.Report `json:"report"`
	}
	if err = json.Unmarshal(record.Data, &checks); err != nil {
		return err
	}
	if checks.Stage != "complete" || !checks.Report.Passed || !sameEvidence(checks.Provenance, t, policyHash) {
		return errors.New("verification evidence is stale or incomplete")
	}
	return nil
}

type evidenceRevision struct {
	task, head, base, policyHash string
	planVersion                  int
}

func sameEvidence(p model.Provenance, t model.Task, hash string) bool {
	actual := evidenceRevision{task: p.Task, head: p.Head, base: p.Base, policyHash: p.PolicyHash, planVersion: p.PlanVersion}
	expected := evidenceRevision{task: t.ID, head: t.ReviewedCommit, base: t.Base, policyHash: hash, planVersion: t.PlanVersion}
	return actual == expected
}
func approvalMatches(t model.Task, hash string) bool {
	return t.Approved && t.Approval != nil && sameEvidence(*t.Approval, t, hash) && t.Approval.ReviewID == t.ReviewID
}
