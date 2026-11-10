package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/process"
	"whirlwind/internal/store"
)

func (a *agentRun) reconcileOpenShellAttempts(ctx context.Context) error {
	if err := a.Engine.reconcileRuntimeJobs(ctx, runtimeCheckOwner{TaskID: a.task.ID}); err != nil {
		return err
	}
	if a.input.Execution == nil {
		return nil
	}
	records, err := a.Store.List(ctx, "sessions")
	if err != nil {
		return fmt.Errorf("load prior runtime ownership: %w", err)
	}
	return a.reconcileRuntimeRecords(ctx, records)
}

func (a *agentRun) reconcileRuntimeRecords(ctx context.Context, records []model.Record) error {
	for _, record := range records {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil {
			return err
		}
		if !sameRecoveryWorkItem(attempt, a) || attempt.AttemptID == a.previousAttempt || attempt.AttemptID == "" {
			continue
		}
		if err := a.reconcilePriorRuntime(ctx, attempt); err != nil {
			return err
		}
	}
	return nil
}

func attemptRuntimeKind(profile *execution.Profile) (string, string) {
	if profile == nil || profile.Launcher == "" {
		return "unknown", ""
	}
	launcher := filepath.Clean(profile.Launcher)
	if execution.IsNonoLauncher(launcher) {
		return "nono", launcher
	}
	if execution.IsManagedLauncher(launcher) {
		return "openshell", launcher
	}
	if strings.Contains(filepath.ToSlash(launcher), "/bridges/sandbox/") {
		return "local", launcher
	}
	return "unknown", launcher
}

func reconcileLocalRuntimeProcessTree(registry string) error {
	report, err := process.Inspect(registry)
	if err != nil {
		return errors.Join(execution.ErrIsolationUnverified, err)
	}
	if !report.Clean {
		return fmt.Errorf("%w: local process registry has %d registration(s) and %d live process(es)", execution.ErrIsolationUnverified, report.Registrations, len(report.Processes))
	}
	return nil
}

func (a *agentAttempt) reconcileOwnedRuntime() error {
	profile := a.input.Execution
	if profile == nil || !execution.IsManagedLauncher(profile.Launcher) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := execution.ReconcileRuntime(ctx, *profile); err != nil {
		return fmt.Errorf("provider returned before its sandbox runtime was isolated: %w", err)
	}
	return nil
}

func sameRecoveryWorkItem(attempt model.Attempt, run *agentRun) bool {
	if attempt.ProjectID != run.project.ID || attempt.PlanVersion != run.task.PlanVersion || attempt.Role != run.input.Role || attempt.Purpose != run.input.Purpose || attempt.UnitID != run.input.UnitID {
		return false
	}
	if run.task.ID != "" {
		return attempt.Task == run.task.ID
	}
	return run.input.OperationID != "" && attempt.OperationID == run.input.OperationID
}

func hasOpenShellIdentity(attempt model.Attempt) bool {
	return attempt.ExecutionID != "" || attempt.ExecutionLabelValue != "" || attempt.SandboxName != "" || attempt.SandboxID != "" || attempt.LifecycleFile != "" || attempt.GatewayName != "" || attempt.GatewayEndpoint != "" || attempt.Workspace != ""
}

func (a *agentRun) reconcilePriorRuntime(ctx context.Context, attempt model.Attempt) error {
	if attempt.RuntimeKind == "nono" && attempt.LifecycleFile == "" {
		if err := reconcileLocalRuntimeProcessTree(filepath.Join(a.Root, "processes")); err != nil {
			return err
		}
		return nil
	}
	if !hasOpenShellIdentity(attempt) {
		return a.reconcileUnpinnedRuntime(attempt)
	}
	profile := execution.Profile{ExecutionID: attempt.ExecutionID, ExecutionGeneration: attempt.ExecutionGeneration, SandboxName: attempt.SandboxName, SandboxID: attempt.SandboxID, LifecycleFile: attempt.LifecycleFile, DeadlineAt: attempt.DeadlineAt, GatewayName: attempt.GatewayName, GatewayEndpoint: attempt.GatewayEndpoint, Workspace: attempt.Workspace, ExecutionLabelValue: attempt.ExecutionLabelValue}
	if err := execution.ReconcileRuntime(ctx, profile); err != nil {
		return errors.Join(fmt.Errorf("reconcile prior attempt %s before replacement: %w", attempt.AttemptID, err), execution.ErrIsolationUnverified)
	}
	return nil
}

func (a *agentRun) reconcileUnpinnedRuntime(attempt model.Attempt) error {
	if attempt.RuntimeKind == "local" && (attempt.Status == "interrupted" || attempt.Status == "failed") {
		if err := reconcileLocalRuntimeProcessTree(filepath.Join(a.Root, "processes")); err != nil {
			return fmt.Errorf("legacy local attempt %s needs process reconciliation: %w", attempt.AttemptID, err)
		}
		return nil
	}
	if attempt.Transport == "acp" && (attempt.Status == "running" || attempt.Status == "interrupted") {
		return fmt.Errorf("%w: legacy attempt %s has no pinned sandbox identity; preserve work and resolve its runtime before retry", execution.ErrIsolationUnverified, attempt.AttemptID)
	}
	return nil

}
