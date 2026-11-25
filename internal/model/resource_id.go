package model

import "github.com/google/uuid"

// ProjectID creates a stable identifier from a checkout path or legacy repository key.
func ProjectID(identity string) string {
	return "project_" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity)).String()
}

// PlanID preserves the one-plan-per-source claim across concurrent intake polls.
func PlanID(source string) string {
	return "plan_" + uuid.NewSHA1(uuid.NameSpaceURL, []byte("whirlwind:plan:"+source)).String()
}

func NewIdeaID() string { return "idea_" + uuid.NewString() }
func NewPlanID() string { return "plan_" + uuid.NewString() }
