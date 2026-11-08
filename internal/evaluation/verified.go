package evaluation

import (
	"context"
	"encoding/json"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func verified(ctx context.Context, s *store.Store, t model.Task) bool {
	if !verificationPrerequisites(t) {
		return false
	}
	evidence, ok := verifiedEvidence(ctx, s, t)
	if !ok {
		return false
	}
	review, ok := completedReview(ctx, s, t.ReviewID)
	if !ok || !sameVerificationReview(evidence.Provenance, review, t) {
		return false
	}
	return noBlockingFindings(review.Findings)
}

type verificationEvidence struct {
	Stage      string           `json:"stage"`
	Provenance model.Provenance `json:"provenance"`
}

func verifiedEvidence(ctx context.Context, s *store.Store, task model.Task) (verificationEvidence, bool) {
	v, err := s.Get(ctx, "verification", task.VerificationID)
	if err != nil {
		return verificationEvidence{}, false
	}
	var evidence verificationEvidence
	if json.Unmarshal(v.Data, &evidence) != nil || evidence.Stage != "complete" {
		return verificationEvidence{}, false
	}
	if !verificationMatchesTask(evidence.Provenance, task) {
		return verificationEvidence{}, false
	}
	return evidence, true
}

func verificationMatchesTask(provenance model.Provenance, task model.Task) bool {
	return verificationTaskPlanMatches(provenance, task) && verificationRevisionMatches(provenance, task)
}

func verificationTaskPlanMatches(provenance model.Provenance, task model.Task) bool {
	return provenance.Task == task.ID && provenance.PlanVersion == task.PlanVersion
}

func verificationRevisionMatches(provenance model.Provenance, task model.Task) bool {
	return provenance.Head == task.ReviewedCommit && provenance.Base == task.Base
}

func completedReview(ctx context.Context, s *store.Store, id string) (model.Review, bool) {
	r, err := s.Get(ctx, "reviews", id)
	if err != nil {
		return model.Review{}, false
	}
	review, err := store.Decode[model.Review](r)
	if err != nil || review.Provenance == nil || review.Score < 9 {
		return model.Review{}, false
	}
	return review, true
}

func sameVerificationReview(provenance model.Provenance, review model.Review, task model.Task) bool {
	return sameReviewRevision(provenance, *review.Provenance) && reviewVerificationID(*review.Provenance, task)
}

func sameReviewRevision(verification, review model.Provenance) bool {
	return sameReviewTaskBase(verification, review) && sameReviewHeadPolicyPlan(verification, review)
}

func sameReviewTaskBase(verification, review model.Provenance) bool {
	return verification.Task == review.Task && verification.Base == review.Base
}

func sameReviewHeadPolicy(verification, review model.Provenance) bool {
	return verification.Head == review.Head && verification.PolicyHash == review.PolicyHash
}

func sameReviewHeadPolicyPlan(verification, review model.Provenance) bool {
	return sameReviewHeadPolicy(verification, review) && verification.PlanVersion == review.PlanVersion
}

func reviewVerificationID(review model.Provenance, task model.Task) bool {
	return review.VerificationID == task.VerificationID
}

func noBlockingFindings(findings []model.Finding) bool {
	for _, f := range findings {
		if f.Blocking && !f.Resolved {
			return false
		}
	}
	return true
}

func verificationPrerequisites(task model.Task) bool {
	return verificationScoreEligible(task) && verificationIDsPresent(task)
}

func verificationScoreEligible(task model.Task) bool {
	return task.ChecksPassed && task.Score >= 9
}

func verificationIDsPresent(task model.Task) bool {
	return hasVerificationReviewIDs(task) && task.ReviewedCommit != ""
}

func hasVerificationReviewIDs(task model.Task) bool {
	return task.VerificationID != "" && task.ReviewID != ""
}
