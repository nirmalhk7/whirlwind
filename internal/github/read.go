package github

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Read retries only callers' idempotent reads. Publication commands must use Run.
func Read(ctx context.Context, cwd, name string, args ...string) (string, error) {
	var output string
	var err error
	for attempt := range 3 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		output, err = Run(attemptCtx, cwd, name, args...)
		if attemptCtx.Err() != nil {
			err = fmt.Errorf("%s read: operation timed out: %w", name, attemptCtx.Err())
		}
		cancel()
		if ctx.Err() != nil {
			return output, ctx.Err()
		}
		if err == nil || !IsTransient(err) || attempt == 2 {
			return output, err
		}
		if err = waitReadRetry(ctx, time.Duration(attempt+1)*time.Second); err != nil {
			return output, err
		}
	}
	return output, err
}

func waitReadRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// IsTransient distinguishes transport outages from repository and permission errors.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "ssh: connect to host ") && strings.Contains(message, "undefined error: 0") {
		return true
	}
	for _, fragment := range []string{
		"connection reset", "connection closed", "closed by remote host", "broken pipe",
		"operation timed out", "connection timed out", "i/o timeout", "tls handshake timeout",
		"error connecting to api.github.com", "could not resolve host", "could not resolve hostname",
		"temporary failure in name resolution", "expected flush after ref listing", "unexpected eof",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func ghRead(ctx context.Context, args ...string) (string, error) {
	return Read(ctx, "", "gh", args...)
}
