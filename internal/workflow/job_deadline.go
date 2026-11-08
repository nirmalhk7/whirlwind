package workflow

import (
	"errors"
	"time"
	"whirlwind/internal/model"
)

// Host-side agent preparation has its own ceiling before any provider attempt.
const preparationJobTimeout = 5 * time.Minute

const attemptWallClockLimit = 5 * time.Minute

func (a *agentRun) attemptTimeoutLimit() time.Duration {
	if a.Engine.attemptTimeout > 0 {
		return a.Engine.attemptTimeout
	}
	return attemptWallClockLimit
}

func attemptDeadline(attempt model.Attempt) (time.Time, error) {
	if attempt.DeadlineAt != "" {
		deadline, err := time.Parse(time.RFC3339Nano, attempt.DeadlineAt)
		if err != nil {
			return time.Time{}, errors.New("invalid persisted attempt deadline")
		}
		return deadline, nil
	}
	if attempt.StartedAt == "" {
		return time.Time{}, errors.New("attempt start time is missing")
	}
	started, err := time.Parse(time.RFC3339Nano, attempt.StartedAt)
	if err != nil {
		return time.Time{}, errors.New("invalid persisted attempt start time")
	}
	return started.Add(attemptWallClockLimit), nil
}

// Planning and discovery use their own persisted attempt deadlines and skip the
// secondary task runtime reservation clock. Connectivity probes stay separate.
func (a *agentRun) timeLimitedPreparation() bool {
	if a.input.Purpose == "provider_test" {
		return false
	}
	return a.input.Role == "ideate" || a.input.Role == "plan"
}
