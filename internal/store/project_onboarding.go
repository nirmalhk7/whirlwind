package store

import (
	"time"

	"whirlwind/internal/model"
)

// InitialProjectWork joins the registration transaction. Both registration
// paths create the same durable work without requiring a plan or another table.
func InitialProjectWork(projectID string, at time.Time) []Change {
	started := at.UTC().Format(time.RFC3339Nano)
	return []Change{
		{Kind: "operations", ID: ID(), Value: model.Operation{Kind: "project_setup", Subject: projectID, Stage: "queued", StartedAt: started, Input: map[string]bool{"onboarding": true}}},
		{Kind: "operations", ID: ID(), Value: model.Operation{Kind: "ideation", Subject: projectID, Stage: "queued", StartedAt: started, Input: map[string]string{"project_id": projectID, "day": at.UTC().Format("2006-01-02")}}},
	}
}
