package workflow

import (
	"context"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/verification"
)

func TestPublicationRejectsChangedCommitPolicyAndPlan(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	task.Base = "base-commit"
	task.ReviewedCommit = "reviewed-commit"
	task.VerificationID = "checks"
	task.ReviewID = "review"
	proof := model.Provenance{Task: task.ID, PlanVersion: task.PlanVersion, Base: task.Base, Head: task.ReviewedCommit, PolicyHash: "reviewed-policy", VerificationID: task.VerificationID, ReviewID: task.ReviewID}
	if err := e.Store.Put(ctx, "verification", task.VerificationID, map[string]any{"stage": "complete", "provenance": proof, "report": verification.Report{Passed: true}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "reviews", task.ReviewID, model.Review{Score: 9, Provenance: &proof}, 0); err != nil {
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
