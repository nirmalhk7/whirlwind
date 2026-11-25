package store

import (
	"time"

	"whirlwind/internal/model"
)

// InitialProjectWork is the default registration work with first-run ideation.
func InitialProjectWork(projectID string, at time.Time) []Change {
	return ProjectRegistrationWork(projectID, at, true)
}

// ProjectRegistrationWork joins setup and any opted-in ideation to the same
// transaction as the project, without first granting a model attempt.
func ProjectRegistrationWork(projectID string, at time.Time, ideate bool) []Change {
	started := at.UTC().Format(time.RFC3339Nano)
	changes := []Change{
		{Kind: "operations", ID: ID(), Value: model.Operation{Kind: "project_setup", Subject: projectID, Stage: "queued", StartedAt: started, Input: map[string]bool{"onboarding": true}}},
	}
	if ideate {
		input := map[string]string{"project_id": projectID, "day": at.UTC().Format("2006-01-02"), "trigger": model.RegistrationIdeationTrigger}
		changes = append(changes, Change{Kind: "operations", ID: ID(), Value: model.Operation{Kind: "ideation", Subject: projectID, Stage: "queued", StartedAt: started, Input: input}})
	}
	return changes
}
