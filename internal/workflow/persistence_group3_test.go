package workflow

import "testing"

func TestPublicationRejectsChangedCommitPolicyAndPlan(t *testing.T) {
	fixture := newAcceptedReviewFixture(t)
	e, task, ctx := fixture.engine, fixture.task, fixture.ctx
	proof := acceptedReviewProvenance(task, fixture.project.PolicyHash)
	fixture.saveReport(t, fixture.report, proof)
	review, adherence, accepted, err := e.LoadAcceptedReviewReport(ctx, task.ID)
	if err != nil || !accepted {
		t.Fatalf("fixture lacks complete accepted review evidence: %v", err)
	}
	review.Adherence, review.Score = &adherence, adherence.Score
	reviewRecord, err := e.Store.Get(ctx, "reviews", task.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "reviews", task.ReviewID, review, reviewRecord.Version); err != nil {
		t.Fatal(err)
	}
	task.Approved = true
	task.Approval = &proof
	if _, err := PublicationEvidence(ctx, e.Store, task, proof.PolicyHash); err != nil || !approvalMatches(task, proof.PolicyHash) {
		t.Fatal("current evidence rejected", err)
	}
	for _, change := range []string{"commit", "policy", "plan", "base"} {
		t.Run(change, func(t *testing.T) {
			changed := task
			hash := proof.PolicyHash
			switch change {
			case "commit":
				changed.ReviewedCommit = "new-commit"
			case "policy":
				hash = "new-policy"
			case "plan":
				changed.PlanVersion++
			case "base":
				changed.Base = "new-base"
			}
			if _, err := PublicationEvidence(ctx, e.Store, changed, hash); err == nil {
				t.Fatal("stale evidence accepted")
			}
			if approvalMatches(changed, hash) {
				t.Fatal("stale approval accepted")
			}
		})
	}
}
