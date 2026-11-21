// Package taskqueue defines the private, native Whirlwind role queue protocol.
package taskqueue

import (
	"context"
	"errors"
	"whirlwind/internal/model"
)

type Role string

const (
	Planner   Role = "planner_tasks"
	Coding    Role = "coding_tasks"
	Reviewer  Role = "reviewer_tasks"
	Finalizer Role = "finalizer_tasks"
)

func Roles() [4]Role { return [...]Role{Planner, Coding, Reviewer, Finalizer} }

func (r Role) Valid() bool {
	return r == Planner || r == Coding || r == Reviewer || r == Finalizer
}

// ForPlan projects readiness; the workflow still revalidates approvals, the
// exact reviewed candidate, and verification evidence at execution.
func ForPlan(p model.ExecutionPlan, policyHash string) Role {
	if !p.HasSelection() {
		return ""
	}
	switch p.Stage {
	case "planning", "plan_ready":
		return Planner
	case "coding", "repair":
		if approved(p) {
			return Coding
		}
	case "review":
		if approved(p) {
			return Reviewer
		}
	case "publishing", "submitting":
		if approved(p) && p.PlanVersion > 0 && p.ChecksPassed && p.Score > 80 &&
			p.ReviewID != "" && p.VerificationID != "" && p.ReviewedCommit != "" &&
			(p.Risk != "critical" || approvedCritical(p, policyHash)) {
			return Finalizer
		}
	}
	return ""
}

func approved(p model.ExecutionPlan) bool {
	return p.Plan != nil && p.PlanDocument != nil && p.PlanDocument.ApprovedHash != ""
}

func approvedCritical(p model.ExecutionPlan, policyHash string) bool {
	a := p.Approval
	return p.Approved && a != nil && a.Task == p.ID && a.PlanVersion == p.PlanVersion &&
		a.Base == p.Base && a.Head == p.ReviewedCommit && a.ReviewID == p.ReviewID && a.PolicyHash == policyHash
}

type Push struct {
	PlanID  string `json:"plan_id,omitempty"`
	Version int    `json:"version,omitempty"`
}

type Receipt struct {
	ID    int64  `json:"id"`
	Token string `json:"token"`
}

type Delivery struct {
	Receipt
	PlanID    string `json:"plan_id"`
	Version   int    `json:"version"`
	Stage     string `json:"stage"`
	ExpiresAt int64  `json:"expires_at_ms"`
}

var ErrConflict = errors.New("queue reference, phase or lease is no longer current")

type Backend interface {
	PushRoleTasks(context.Context, Role, Push) error
	PopRoleTask(context.Context, Role) (*Delivery, error)
	SettleRoleTask(context.Context, Role, Receipt, string) error
}
