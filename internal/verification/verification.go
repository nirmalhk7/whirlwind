package verification

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

type Result struct {
	Runtime          RuntimeIdentity `json:"runtime,omitempty"`
	ExecutionProfile string          `json:"execution_profile,omitempty"`
	ReusedFrom       string          `json:"reused_from,omitempty"`
	Command          []string        `json:"command"`
	TimeoutSeconds   int             `json:"timeout_seconds"`
	StartedAt        string          `json:"started_at"`
	FinishedAt       string          `json:"finished_at"`
	ExitCode         *int            `json:"exit_code"`
	Truncated        bool            `json:"truncated"`
	OutputArtifact   string          `json:"output_artifact,omitempty"`
	Diagnostics      string          `json:"diagnostics,omitempty"`
	Name             string          `json:"name"`
	Passed           bool            `json:"passed"`
	Output           string          `json:"output"`
	Error            string          `json:"error,omitempty"`
	DurationMS       int64           `json:"duration_ms"`
}

type RuntimeIdentity struct {
	ExecutionID     string `json:"execution_id,omitempty"`
	Launcher        string `json:"launcher,omitempty"`
	LifecycleFile   string `json:"lifecycle_file,omitempty"`
	SandboxName     string `json:"sandbox_name,omitempty"`
	SandboxID       string `json:"sandbox_id,omitempty"`
	GatewayName     string `json:"gateway_name,omitempty"`
	GatewayEndpoint string `json:"gateway_endpoint,omitempty"`
	Workspace       string `json:"workspace,omitempty"`
	Kind            string `json:"kind,omitempty"`
	CleanupVerified bool   `json:"cleanup_verified,omitempty"`
	Generation      int    `json:"generation,omitempty"`
}
type Report struct {
	Passed  bool     `json:"passed"`
	Results []Result `json:"results"`
}

// Run executes checks sequentially with per-check timeouts, captures bounded diagnostics, and marks the report failed on any error.
func Run(ctx context.Context, cwd string, checks []model.Check) Report {
	report, _ := RunObserved(ctx, cwd, checks, nil)
	return report
}

func RunObserved(ctx context.Context, cwd string, checks []model.Check, observe func(Result) error) (Report, error) {
	profile, err := execution.ForPolicy(cwd, false, "", nil)
	if err != nil {
		return Report{Passed: false}, err
	}
	return RunObservedWithProfile(ctx, cwd, checks, profile, observe)
}

// RunObservedWithProfile uses the same enforced sandbox as providers for every committed check.
func RunObservedWithProfile(ctx context.Context, cwd string, checks []model.Check, profile execution.Profile, observe func(Result) error) (Report, error) {
	out := Report{Passed: len(checks) > 0, Results: []Result{}}
	for index, c := range checks {
		r, setupFailed := runObservedCheck(ctx, cwd, c, profile, index, len(checks))
		out.Results = append(out.Results, r)
		out.Passed = out.Passed && r.Passed
		if observe != nil {
			if err := observe(r); err != nil {
				out.Passed = false
				return out, err
			}
		}
		if setupFailed {
			continue
		}
		if ctx.Err() != nil {
			out.Passed = false
			return out, ctx.Err()
		}
	}
	return out, nil
}

func runObservedCheck(ctx context.Context, cwd string, check model.Check, profile execution.Profile, index, count int) (Result, bool) {
	checkProfile, err := checkRuntimeProfile(ctx, profile, index, count, check)
	if err != nil {
		return failedCheck(check, err), true
	}
	result := runCheckProfile(ctx, cwd, check, checkProfile)
	if execution.IsManagedLauncher(checkProfile.Launcher) {
		result = reconcileCheckRuntime(result, checkProfile)
	}
	return result, false
}

func failedCheck(check model.Check, err error) Result {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return Result{Name: check.Name, Command: check.Command, StartedAt: now, FinishedAt: now, Error: err.Error()}
}

func checkRuntimeProfile(ctx context.Context, profile execution.Profile, index, count int, check model.Check) (execution.Profile, error) {
	if !execution.IsManagedLauncher(profile.Launcher) {
		return profile, nil
	}
	profile, err := checkProfileIdentity(ctx, profile, index, count, check)
	if err != nil {
		return profile, err
	}
	if _, exists, err := execution.ReadRuntimeLifecycle(profile); err != nil {
		return profile, err
	} else if exists {
		return profile, nil
	}
	return execution.PinRuntime(ctx, profile, profile.ExecutionID, profile.ExecutionGeneration, profile.DeadlineAt, profile.SupervisorDir)
}

func checkProfileIdentity(ctx context.Context, profile execution.Profile, index, count int, check model.Check) (execution.Profile, error) {
	var err error
	profile, err = ensureCheckSupervisorDir(profile)
	if err != nil {
		return profile, err
	}
	profile, err = assignCheckExecutionID(profile, index, count)
	if err != nil {
		return profile, err
	}
	if profile.ExecutionGeneration < 1 {
		profile.ExecutionGeneration = 1
	}
	if profile.DeadlineAt == "" {
		profile.DeadlineAt = checkDeadline(ctx, check)
	}
	return profile, nil
}

func ensureCheckSupervisorDir(profile execution.Profile) (execution.Profile, error) {
	if profile.SupervisorDir == "" {
		root, err := os.MkdirTemp("", "whirlwind-check-runtime-")
		if err != nil {
			return profile, err
		}
		profile.SupervisorDir = filepath.Join(root, "executions")
	}
	return profile, nil
}

func assignCheckExecutionID(profile execution.Profile, index, count int) (execution.Profile, error) {
	if profile.ExecutionID == "" {
		id, err := randomExecutionID()
		if err != nil {
			return profile, err
		}
		profile.ExecutionID = id
	} else if count > 1 {
		profile.ExecutionID += "-check-" + strconv.Itoa(index+1)
	}
	return profile, nil
}

func checkDeadline(ctx context.Context, check model.Check) string {
	timeout := time.Duration(check.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	if parentDeadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(parentDeadline)
		if remaining < timeout {
			timeout = remaining
		}
	}
	return time.Now().Add(timeout).UTC().Format(time.RFC3339Nano)
}

func randomExecutionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "check-" + hex.EncodeToString(raw[:]), nil
}

func reconcileCheckRuntime(result Result, profile execution.Profile) Result {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cleanupErr := execution.ReconcileRuntime(cleanupCtx, profile)
	identity := RuntimeIdentity{ExecutionID: profile.ExecutionID, Generation: profile.ExecutionGeneration,
		Kind: "openshell", Launcher: profile.Launcher, SandboxName: profile.SandboxName,
		LifecycleFile: profile.LifecycleFile, GatewayName: profile.GatewayName,
		GatewayEndpoint: profile.GatewayEndpoint, Workspace: profile.Workspace}
	if lifecycle, exists, err := execution.ReadRuntimeLifecycle(profile); err == nil && exists {
		identity.SandboxID = lifecycle.SandboxID
	}
	if execution.IsNonoLauncher(profile.Launcher) {
		identity.Kind = "nono"
	}
	identity.CleanupVerified = cleanupErr == nil
	result.Runtime = identity
	if cleanupErr != nil {
		result.Passed = false
		result.Error = errors.Join(errors.New(result.Error), cleanupErr).Error()
	}
	return result
}
