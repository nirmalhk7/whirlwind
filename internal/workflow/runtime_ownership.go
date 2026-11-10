package workflow

import (
	"context"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func nextExecutionGeneration(ctx context.Context, database *store.Store, episodeID string) (int, error) {
	records, err := database.List(ctx, "sessions")
	if err != nil {
		return 0, err
	}
	generation := 0
	for _, record := range records {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil {
			return 0, err
		}
		if attempt.RecoveryEpisodeID == episodeID && attempt.ExecutionGeneration > generation {
			generation = attempt.ExecutionGeneration
		}
	}
	return generation + 1, nil
}

func sameRuntimeOwner(current, expected model.Attempt) bool {
	return !current.Finished && current.Status == "running" && sameAttemptOwner(current, expected)
}

func runtimeLeaseActive(current model.Attempt, at time.Time) bool {
	expires, err := time.Parse(time.RFC3339Nano, current.LeaseExpiresAt)
	return err == nil && at.Before(expires)
}

func sameAttemptOwner(current, expected model.Attempt) bool {
	return current.AttemptID == expected.AttemptID && current.ExecutionID == expected.ExecutionID && current.Owner == expected.Owner && current.ExecutionGeneration == expected.ExecutionGeneration
}
