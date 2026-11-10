package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const standaloneExecutionTimeout = 5 * time.Minute

func defaultStandaloneSupervisorDir() (string, error) {
	if configured := os.Getenv("WHIRLWIND_DATA_DIR"); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", fmt.Errorf("%w: standalone runtime data directory must be absolute", execution.ErrSandbox)
		}
		return filepath.Join(configured, "supervisor", "standalone"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%w: standalone runtime home unavailable", execution.ErrSandbox)
	}
	return filepath.Join(home, ".local", "share", "whirlwind", "supervisor", "standalone"), nil
}

func pinStandaloneProfile(ctx context.Context, profile execution.Profile, supervisorDir string) (execution.Profile, error) {
	if !execution.IsManagedLauncher(profile.Launcher) {
		return profile, nil
	}
	if supervisorDir == "" {
		var err error
		supervisorDir, err = defaultStandaloneSupervisorDir()
		if err != nil {
			return execution.Profile{}, err
		}
	}
	profile.SupervisorDir = supervisorDir
	deadline := time.Now().Add(standaloneExecutionTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if !deadline.After(time.Now()) {
		return execution.Profile{}, context.DeadlineExceeded
	}
	pinned, err := execution.PinRuntime(ctx, profile, store.ID(), 1, deadline.UTC().Format(time.RFC3339Nano), supervisorDir)
	if err != nil {
		return execution.Profile{}, err
	}
	if err := execution.RegisterStandaloneRuntime(pinned); err != nil {
		return execution.Profile{}, err
	}
	pinned.StandaloneRuntime = true
	return pinned, nil
}

func completeStandaloneProfile(profile execution.Profile) error {
	if !profile.StandaloneRuntime {
		return nil
	}
	return execution.CompleteStandaloneRuntime(profile)
}

func retainStandaloneProfile(profile execution.Profile, cleanupErr error) error {
	if !profile.StandaloneRuntime || cleanupErr == nil {
		return cleanupErr
	}
	return errors.Join(cleanupErr, execution.MarkStandaloneCleanupPending(profile))
}

func pinStandaloneForProvider(ctx context.Context, cwd string, readOnly bool, policy model.Policy, command []string, supervisorDir string) (execution.Profile, error) {
	profile, err := execution.ForProvider(cwd, readOnly, "", policy.Sandbox, command)
	if err != nil {
		return execution.Profile{}, err
	}
	profile.AllowedPaths = append([]string{}, policy.AllowedPaths...)
	return pinStandaloneProfile(ctx, profile, supervisorDir)
}
