package workflow

import (
	"context"
	"encoding/json"
	"errors"

	"whirlwind/internal/model"
	"whirlwind/internal/verification"
)

func verificationConfigurationHash(plan model.ExecutionPlan) string {
	if plan.Plan != nil && plan.Plan.Approval != nil {
		return plan.Plan.Approval.ConfigurationHash
	}
	if plan.PlanDocument != nil {
		return plan.PlanDocument.PolicyHash
	}
	return ""
}

// recoverVerification finds an unfinished journal even when the plan's final
// VerificationID write was lost. Results remain bound to the original manifest.
func (e *Engine) recoverVerification(ctx context.Context, plan *model.ExecutionPlan, head string) (*verificationRun, error) {
	if plan.IdeaID == "" || plan.PlanDocument == nil {
		return nil, nil
	}
	records, err := e.Store.List(ctx, "verification")
	if err != nil {
		return nil, err
	}
	var recovered *verificationRun
	for _, record := range records {
		matches, err := unfinishedVerificationMatches(record, plan, head)
		if err != nil {
			return nil, err
		}
		if !matches {
			continue
		}
		if recovered != nil {
			return nil, errors.New("multiple unfinished verification journals match candidate; inspect before resuming")
		}
		var value map[string]any
		if err := json.Unmarshal(record.Data, &value); err != nil {
			return nil, err
		}
		recovered = &verificationRun{engine: e, ctx: ctx, task: plan, id: record.ID, head: head, value: value, version: record.Version}
	}
	if recovered != nil {
		recovered.value["stage"], recovered.value["owner"] = "running", e.owner
		delete(recovered.value, "failure_kind")
		delete(recovered.value, "error")
		delete(recovered.value, "finished_at")
		if err := recovered.save(); err != nil {
			return nil, err
		}
		plan.VerificationID = recovered.id
	}
	return recovered, nil
}

func unfinishedVerificationMatches(record model.Record, plan *model.ExecutionPlan, head string) (bool, error) {
	var saved struct {
		Stage       string                    `json:"stage"`
		FailureKind string                    `json:"failure_kind"`
		Manifest    *verification.JobManifest `json:"runner_manifest"`
	}
	if err := json.Unmarshal(record.Data, &saved); err != nil {
		return false, err
	}
	m := saved.Manifest
	if m == nil || m.JobID != record.ID || m.PlanID != plan.ID || m.PlanVersion != plan.PlanVersion || m.PlanHash != plan.PlanDocument.ApprovedHash || m.ConfigurationHash != verificationConfigurationHash(*plan) || m.CandidateCommit != head || m.BaseCommit != plan.Base || m.Repository != plan.Worktree {
		return false, nil
	}
	if saved.Stage != "running" && (saved.Stage != "failed" || saved.FailureKind != "interrupted") {
		return false, nil
	}
	return true, nil
}
