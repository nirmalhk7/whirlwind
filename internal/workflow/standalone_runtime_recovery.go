package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"whirlwind/internal/execution"
)

func (e *Engine) reconcileStandaloneRuntimes(ctx context.Context) error {
	root := filepath.Join(e.Root, "supervisor", "standalone")
	entries, err := execution.ReadStandaloneRuntimes(root)
	if err != nil {
		return fmt.Errorf("read standalone runtime registry: %w", err)
	}
	for _, locator := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := reconcileStandaloneRuntime(ctx, root, locator); err != nil {
			return err
		}
	}
	return nil
}

func reconcileStandaloneRuntime(ctx context.Context, root string, locator execution.StandaloneRuntimeLocator) error {
	profile, err := execution.StandaloneRuntimeProfile(root, locator)
	if err != nil {
		return err
	}
	live, err := retainedStandaloneRuntimeIsLive(locator)
	if err != nil || live {
		return err
	}
	if err := restoreStandaloneSandboxID(&profile); err != nil {
		return err
	}
	if err := execution.ReconcileRuntime(ctx, profile); err != nil {
		return fmt.Errorf("reconcile standalone runtime %s before replacement: %w", locator.ExecutionID, err)
	}
	locator, err = reconciledStandaloneLocator(root, locator)
	if err != nil {
		return err
	}
	if err := execution.CompleteStandaloneLocator(root, locator); err != nil {
		return fmt.Errorf("remove reconciled standalone runtime %s: %w", locator.ExecutionID, err)
	}
	return nil
}

func retainedStandaloneRuntimeIsLive(locator execution.StandaloneRuntimeLocator) (bool, error) {
	currentOwner, err := execution.StandaloneRuntimeOwnerIsCurrent(locator)
	if err != nil {
		return false, err
	}
	deadline, err := time.Parse(time.RFC3339Nano, locator.DeadlineAt)
	if err != nil {
		return false, fmt.Errorf("%w: registered standalone runtime deadline is invalid", execution.ErrIsolationUnverified)
	}
	if currentOwner && locator.State == "active" && deadline.After(time.Now()) {
		return true, nil
	}
	if currentOwner && locator.State == "cleanup_pending" && deadline.After(time.Now()) {
		return false, fmt.Errorf("%w: current standalone runtime has unverified cleanup", execution.ErrIsolationUnverified)
	}
	return false, nil
}

func restoreStandaloneSandboxID(profile *execution.Profile) error {
	lifecycle, exists, err := execution.ReadRuntimeLifecycle(*profile)
	if err != nil {
		return errors.Join(execution.ErrIsolationUnverified, fmt.Errorf("read standalone runtime lifecycle: %w", err))
	}
	if exists {
		if profile.SandboxID != "" && lifecycle.SandboxID != "" && profile.SandboxID != lifecycle.SandboxID {
			return fmt.Errorf("%w: standalone registry and lifecycle sandbox IDs differ", execution.ErrIsolationUnverified)
		}
		if lifecycle.SandboxID != "" {
			profile.SandboxID = lifecycle.SandboxID
		}
	}
	return nil
}

func reconciledStandaloneLocator(root string, expected execution.StandaloneRuntimeLocator) (execution.StandaloneRuntimeLocator, error) {
	entries, err := execution.ReadStandaloneRuntimes(root)
	if err != nil {
		return execution.StandaloneRuntimeLocator{}, fmt.Errorf("reload standalone runtime registry after cleanup: %w", err)
	}
	for _, current := range entries {
		if current.ExecutionID != expected.ExecutionID {
			continue
		}
		expected.SandboxID = current.SandboxID
		if current != expected {
			return execution.StandaloneRuntimeLocator{}, fmt.Errorf("%w: standalone runtime registry authority changed during cleanup", execution.ErrIsolationUnverified)
		}
		return current, nil
	}
	return execution.StandaloneRuntimeLocator{}, fmt.Errorf("%w: standalone runtime registry entry disappeared during cleanup", execution.ErrIsolationUnverified)
}
