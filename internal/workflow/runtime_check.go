package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

var ErrRuntimeCheckOutcomeUncertain = errors.New("check outcome is uncertain after runtime interruption")

type runtimeCheckOwner struct {
	TaskID         string
	AttemptID      string
	VerificationID string
	Role           string
}

type runtimeCheckJob struct {
	Stage               string `json:"stage"`
	ExecutionID         string `json:"execution_id"`
	ExecutionGeneration int    `json:"execution_generation"`
	ExecutionLabelValue string `json:"execution_label_value,omitempty"`
	DeadlineAt          string `json:"deadline_at"`
	SupervisorDir       string `json:"supervisor_dir"`
	LifecycleFile       string `json:"lifecycle_file"`
	SandboxName         string `json:"sandbox_name"`
	SandboxID           string `json:"sandbox_id,omitempty"`
	GatewayName         string `json:"gateway_name,omitempty"`
	GatewayEndpoint     string `json:"gateway_endpoint,omitempty"`
	Workspace           string `json:"workspace,omitempty"`
	CleanupVerified     bool   `json:"cleanup_verified,omitempty"`
	Error               string `json:"error,omitempty"`
	TaskID              string `json:"task_id,omitempty"`
	AttemptID           string `json:"attempt_id,omitempty"`
	VerificationID      string `json:"verification_id,omitempty"`
	Role                string `json:"role,omitempty"`
	CheckID             string `json:"check_id"`
	StartedAt           string `json:"started_at"`
}

func (e *Engine) runOwnedCheck(ctx context.Context, cwd string, check model.Check, profile execution.Profile, owner runtimeCheckOwner) (verification.Result, error) {
	if !execution.IsOpenShellLauncher(profile.Launcher) {
		report, err := verification.RunObservedWithProfile(ctx, cwd, []model.Check{check}, profile, nil)
		if err != nil || len(report.Results) != 1 {
			return verification.Result{}, fmt.Errorf("check %s returned no complete result: %w", check.Name, err)
		}
		return report.Results[0], nil
	}
	if err := e.reconcileRuntimeJobs(ctx, owner); err != nil {
		return verification.Result{}, err
	}
	job, version, err := e.beginRuntimeCheck(ctx, check, owner)
	if err != nil {
		return verification.Result{}, err
	}
	profile.SupervisorDir = job.SupervisorDir
	profile.ExecutionID = job.ExecutionID
	profile.ExecutionGeneration = job.ExecutionGeneration
	profile.DeadlineAt = job.DeadlineAt
	pinned, err := execution.PinOpenShellRuntime(ctx, profile, job.ExecutionID, job.ExecutionGeneration, job.DeadlineAt, job.SupervisorDir)
	if err != nil {
		return verification.Result{}, e.finishRuntimeCheck(ctx, job, version, err, verification.RuntimeIdentity{})
	}
	job = runtimeCheckJobForProfile(job, pinned)
	version, err = e.persistRuntimeCheck(ctx, job, version)
	if err != nil {
		return verification.Result{}, err
	}
	report, runErr := verification.RunObservedWithProfile(ctx, cwd, []model.Check{check}, pinned, nil)
	if len(report.Results) == 1 {
		result := report.Results[0]
		if finishErr := e.finishRuntimeCheck(ctx, job, version, resultError(result), result.Runtime); finishErr != nil {
			return result, finishErr
		}
		if runErr != nil {
			return result, runErr
		}
		return result, nil
	}
	if runErr != nil || len(report.Results) != 1 {
		if runErr == nil {
			runErr = fmt.Errorf("check %s returned no complete result", check.Name)
		}
		return verification.Result{}, e.finishRuntimeCheck(ctx, job, version, runErr, verification.RuntimeIdentity{})
	}
	return verification.Result{}, nil
}

func (e *Engine) reconcileRuntimeJobs(ctx context.Context, owner runtimeCheckOwner) error {
	rows, err := e.Store.List(ctx, "runtime_jobs")
	if err != nil {
		return err
	}
	var unresolved []error
	for _, row := range rows {
		job, err := store.Decode[runtimeCheckJob](row)
		if err != nil {
			return fmt.Errorf("decode check runtime job %s: %w", row.ID, err)
		}
		if !runtimeJobMayBlock(job, owner) {
			continue
		}
		if err := reconcileInterruptedRuntimeJob(ctx, job); err != nil {
			pauseErr := e.pauseRuntimeCheckTask(ctx, job, err)
			unresolved = append(unresolved, errors.Join(err, pauseErr))
			continue
		}
		job.Stage = "uncertain"
		job.CleanupVerified = true
		job.Error = ErrRuntimeCheckOutcomeUncertain.Error()
		if err := e.Store.Put(context.WithoutCancel(ctx), "runtime_jobs", row.ID, job, row.Version); err != nil {
			unresolved = append(unresolved, fmt.Errorf("persist interrupted check reconciliation: %w", err))
			continue
		}
		if err := e.pauseRuntimeCheckTask(ctx, job, ErrRuntimeCheckOutcomeUncertain); err != nil {
			unresolved = append(unresolved, err)
			continue
		}
		unresolved = append(unresolved, fmt.Errorf("%w: check %s may have left partial effects", ErrRuntimeCheckOutcomeUncertain, job.CheckID))
	}
	return errors.Join(unresolved...)
}

func runtimeJobMayBlock(job runtimeCheckJob, owner runtimeCheckOwner) bool {
	if job.Stage != "running" && job.Stage != "uncertain" {
		return false
	}
	if owner.TaskID == "" && owner.AttemptID == "" && owner.VerificationID == "" {
		return true
	}
	if owner.TaskID != "" && job.TaskID == owner.TaskID {
		return true
	}
	if owner.AttemptID != "" && job.AttemptID == owner.AttemptID {
		return true
	}
	return owner.VerificationID != "" && job.VerificationID == owner.VerificationID
}

func reconcileInterruptedRuntimeJob(ctx context.Context, job runtimeCheckJob) error {
	data, err := os.ReadFile(job.LifecycleFile)
	if err != nil {
		return errors.Join(execution.ErrIsolationUnverified, err)
	}
	var lifecycle execution.SandboxLifecycle
	if err := json.Unmarshal(data, &lifecycle); err != nil || !runtimeJobLifecycleMatches(job, lifecycle) {
		return execution.ErrIsolationUnverified
	}
	profile := execution.Profile{ExecutionID: job.ExecutionID, ExecutionGeneration: job.ExecutionGeneration,
		ExecutionLabelValue: job.ExecutionLabelValue, DeadlineAt: job.DeadlineAt, SandboxName: job.SandboxName,
		LifecycleFile: job.LifecycleFile, SandboxID: lifecycle.SandboxID, GatewayName: lifecycle.GatewayName,
		GatewayEndpoint: lifecycle.GatewayEndpoint, Workspace: lifecycle.Workspace}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return execution.ReconcileOpenShellRuntime(cleanupCtx, profile)
}

func runtimeJobLifecycleMatches(job runtimeCheckJob, lifecycle execution.SandboxLifecycle) bool {
	return lifecycle.ExecutionID == job.ExecutionID && lifecycle.ExecutionGeneration == job.ExecutionGeneration &&
		lifecycle.ExecutionLabelValue == job.ExecutionLabelValue && lifecycle.DeadlineAt == job.DeadlineAt &&
		lifecycle.SandboxName == job.SandboxName && lifecycle.GatewayName != "" && lifecycle.GatewayEndpoint != "" && lifecycle.Workspace != "" &&
		(job.GatewayName == "" || job.GatewayName == lifecycle.GatewayName) &&
		(job.GatewayEndpoint == "" || job.GatewayEndpoint == lifecycle.GatewayEndpoint) &&
		(job.Workspace == "" || job.Workspace == lifecycle.Workspace) &&
		(job.SandboxID == "" || job.SandboxID == lifecycle.SandboxID)
}

func (e *Engine) pauseRuntimeCheckTask(ctx context.Context, job runtimeCheckJob, failure error) error {
	if job.TaskID == "" {
		return nil
	}
	record, err := e.Store.Get(ctx, "tasks", job.TaskID)
	if err != nil {
		return err
	}
	task, err := store.Decode[model.Task](record)
	if err != nil || task.Stage == "paused" || task.Stage == "plan_approval" || task.Cancelled() {
		return err
	}
	task.ResumeStage = task.Stage
	task.Stage = "paused"
	task.Error = failure.Error()
	return e.Store.Put(context.WithoutCancel(ctx), "tasks", task.ID, task, record.Version)
}

func (e *Engine) beginRuntimeCheck(ctx context.Context, check model.Check, owner runtimeCheckOwner) (runtimeCheckJob, int, error) {
	now := time.Now().UTC()
	deadline := now.Add(runtimeCheckDeadline(ctx, check))
	job := runtimeCheckJob{Stage: "running", ExecutionID: store.ID(), ExecutionGeneration: 1,
		DeadlineAt: deadline.Format(time.RFC3339Nano), SupervisorDir: filepath.Join(e.Root, "supervisor", "executions"),
		TaskID: owner.TaskID, AttemptID: owner.AttemptID, VerificationID: owner.VerificationID, Role: owner.Role,
		CheckID: check.Name, StartedAt: now.Format(time.RFC3339Nano)}
	job.SandboxName = execution.OpenShellSandboxName(job.ExecutionID)
	job.ExecutionLabelValue = execution.OpenShellExecutionLabel(job.ExecutionID, job.ExecutionGeneration)
	job.LifecycleFile = filepath.Join(job.SupervisorDir, job.ExecutionID, "openshell.json")
	if err := e.Store.Put(ctx, "runtime_jobs", job.ExecutionID, job, 0); err != nil {
		return runtimeCheckJob{}, 0, fmt.Errorf("persist check runtime locator: %w", err)
	}
	return job, 1, nil
}

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

func runtimeCheckJobForProfile(job runtimeCheckJob, profile execution.Profile) runtimeCheckJob {
	job.ExecutionLabelValue = profile.ExecutionLabelValue
	job.LifecycleFile = profile.LifecycleFile
	job.SandboxName = profile.SandboxName
	job.GatewayName = profile.GatewayName
	job.GatewayEndpoint = profile.GatewayEndpoint
	job.Workspace = profile.Workspace
	return job
}

func (e *Engine) persistRuntimeCheck(ctx context.Context, job runtimeCheckJob, version int) (int, error) {
	if err := e.Store.Put(ctx, "runtime_jobs", job.ExecutionID, job, version); err != nil {
		return version, fmt.Errorf("persist pinned check runtime identity: %w", err)
	}
	return version + 1, nil
}

func (e *Engine) finishRuntimeCheck(ctx context.Context, job runtimeCheckJob, version int, runErr error, identity verification.RuntimeIdentity) error {
	job.SandboxID = identity.SandboxID
	job.CleanupVerified = identity.CleanupVerified
	job.Stage = "complete"
	if runErr != nil {
		job.Error = runErr.Error()
	}
	if !identity.CleanupVerified {
		job.Stage = "uncertain"
		if runErr == nil {
			runErr = execution.ErrIsolationUnverified
		}
	}
	ctx = context.WithoutCancel(ctx)
	if err := e.Store.Put(ctx, "runtime_jobs", job.ExecutionID, job, version); err != nil {
		return errors.Join(runErr, fmt.Errorf("persist check runtime outcome: %w", err))
	}
	if !identity.CleanupVerified {
		return errors.Join(runErr, execution.ErrIsolationUnverified)
	}
	return nil
}

func resultError(result verification.Result) error {
	if result.Error == "" {
		return nil
	}
	return errors.New(result.Error)
}
