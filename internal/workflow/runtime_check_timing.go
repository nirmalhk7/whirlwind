package workflow

import (
	"context"
	"errors"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/verification"
)

func runtimeCheckDeadline(ctx context.Context, check model.Check) time.Duration {
	timeout := time.Duration(check.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	if parentDeadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(parentDeadline)
		if remaining < timeout {
			return remaining
		}
	}
	return timeout
}

func resultError(result verification.Result) error {
	if result.Error == "" {
		return nil
	}
	return errors.New(result.Error)
}
