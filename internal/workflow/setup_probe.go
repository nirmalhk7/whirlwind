package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type setupProbe struct {
	Active      bool   `json:"active"`
	Error       string `json:"error"`
	CheckedAt   string `json:"checked_at"`
	NextCheckAt string `json:"next_check_at"`
}

func (e *Engine) setupProbeResult(parent context.Context, p model.Project, b setupBlock) (setupProbe, error) {
	record, err := e.Store.Get(parent, "setup_probes", b.Fingerprint)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return setupProbe{}, err
	}
	if err == nil {
		cached, err := store.Decode[setupProbe](record)
		if err != nil {
			return setupProbe{}, err
		}
		next, _ := time.Parse(time.RFC3339Nano, cached.NextCheckAt)
		if time.Now().Before(next) {
			return cached, nil
		}
	}
	return e.executeSetupProbe(parent, p, b, record.Version)
}

func (e *Engine) executeSetupProbe(parent context.Context, p model.Project, b setupBlock, version int) (setupProbe, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	if err := e.acquire(ctx, p); err != nil {
		return setupProbe{}, err
	}
	defer e.release(p.ID)
	input, cleanupWorktree, err := e.setupProbeInput(ctx, p, b)
	var pinned *execution.Profile
	if err == nil {
		input, pinned, err = e.pinSetupProbeRuntime(ctx, input)
	}
	if err == nil {
		err = (provider.ProcessAdapter{Provider: b.Source}).CheckReadiness(ctx, input)
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	cleanupErr := e.reconcileSetupProbeRuntime(cleanupCtx, pinned)
	cleanupCancel()
	if cleanupErr != nil {
		err = errors.Join(err, cleanupErr)
	} else if cleanupWorktree != nil {
		if worktreeErr := cleanupWorktree(); worktreeErr != nil {
			err = errors.Join(err, worktreeErr)
		}
	}
	if parent.Err() != nil {
		return setupProbe{}, parent.Err()
	}
	seconds := e.settings(parent).ProviderStatusCheckSeconds
	if seconds < 1 {
		seconds = 30
	}
	now := time.Now().UTC()
	result := setupProbe{Active: err != nil, CheckedAt: now.Format(time.RFC3339Nano), NextCheckAt: now.Add(time.Duration(seconds) * time.Second).Format(time.RFC3339Nano)}
	if err != nil {
		result.Error = err.Error()
	}
	return result, e.Store.Put(parent, "setup_probes", b.Fingerprint, result, version)
}

func (e *Engine) setupProbeInput(ctx context.Context, p model.Project, b setupBlock) (provider.Input, func() error, error) {
	revision, err := github.Run(ctx, p.Path, "git", "rev-parse", "HEAD")
	if err != nil {
		return provider.Input{}, nil, fmt.Errorf("resolve setup probe snapshot: %w", err)
	}
	cwd, remove, err := e.ideationWorktree(ctx, p, revision, "setup-probe-"+b.Fingerprint, "readiness", 0)
	if err != nil {
		return provider.Input{}, nil, fmt.Errorf("create owned setup probe snapshot: %w", err)
	}
	cleanup := func() error {
		remove()
		if _, statErr := os.Lstat(cwd); !os.IsNotExist(statErr) {
			return fmt.Errorf("setup probe worktree cleanup is unverified: %s: %w", cwd, statErr)
		}
		return nil
	}
	input := b.Input
	input.CWD = cwd
	input.Policy = p.Policy
	input.ReadOnly = true
	input.Execution = nil
	input.ExecutionProfile = ""
	run := agentRun{Engine: e, ctx: ctx, project: p, task: model.Task{ID: "setup-probe:" + p.ID}, input: input}
	if err := run.prepareExecutionProfile(b.Source); err != nil {
		return provider.Input{}, cleanup, err
	}
	if b.Input.Execution != nil {
		run.input.Execution.RuntimeDir = b.Input.Execution.RuntimeDir
	}
	return run.input, cleanup, nil
}

func (e *Engine) pinSetupProbeRuntime(ctx context.Context, input provider.Input) (provider.Input, *execution.Profile, error) {
	if input.Execution == nil || !execution.IsManagedLauncher(input.Execution.Launcher) {
		return input, nil, nil
	}
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.After(time.Now()) {
		return input, nil, fmt.Errorf("%w: setup probe requires an active deadline", execution.ErrSandbox)
	}
	supervisorDir := filepath.Join(e.Root, "supervisor", "standalone")
	profile := *input.Execution
	profile.SupervisorDir = supervisorDir
	pinned, err := execution.PinRuntime(ctx, profile, "setup-probe-"+store.ID(), 1, deadline.UTC().Format(time.RFC3339Nano), supervisorDir)
	if err != nil {
		return input, nil, err
	}
	if err := execution.RegisterStandaloneRuntime(pinned); err != nil {
		return input, &pinned, err
	}
	pinned.StandaloneRuntime = true
	input.Execution = &pinned
	return input, &pinned, nil
}

func (e *Engine) reconcileSetupProbeRuntime(ctx context.Context, pinned *execution.Profile) error {
	if pinned == nil || !pinned.StandaloneRuntime {
		return nil
	}
	entries, err := execution.ReadStandaloneRuntimes(pinned.SupervisorDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.ExecutionID != pinned.ExecutionID {
			continue
		}
		if !setupProbeLocatorMatches(entry, *pinned) {
			return fmt.Errorf("%w: setup probe runtime identity changed before cleanup", execution.ErrIsolationUnverified)
		}
		owned, err := execution.StandaloneRuntimeOwnerIsCurrent(entry)
		if err != nil || !owned {
			return errors.Join(execution.ErrIsolationUnverified, err)
		}
		profile, err := execution.StandaloneRuntimeProfile(pinned.SupervisorDir, entry)
		if err != nil {
			return err
		}
		if err := execution.ReconcileRuntime(ctx, profile); err != nil {
			return errors.Join(err, execution.MarkStandaloneCleanupPending(profile))
		}
		return execution.CompleteStandaloneRuntime(profile)
	}
	return nil
}

func setupProbeLocatorMatches(locator execution.StandaloneRuntimeLocator, profile execution.Profile) bool {
	return locator.ExecutionID == profile.ExecutionID && locator.Generation == profile.ExecutionGeneration &&
		locator.LifecycleFile == profile.LifecycleFile && locator.SandboxName == profile.SandboxName &&
		locator.GatewayName == profile.GatewayName && locator.GatewayEndpoint == profile.GatewayEndpoint &&
		locator.Workspace == profile.Workspace && locator.Launcher == profile.Launcher &&
		locator.CWD == profile.CWD && locator.DeadlineAt == profile.DeadlineAt
}
