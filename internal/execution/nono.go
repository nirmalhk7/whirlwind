package execution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"whirlwind/internal/process"
)

// The host chooses the runtime; agent policy cannot override this bootstrap.
func nonoCLI() (string, error) {
	name := os.Getenv("WHIRLWIND_NONO_CLI")
	if name == "" {
		name = "nono"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: nono CLI unavailable; install nono 0.81.0: %v", ErrSandbox, err)
	}
	return path, nil
}

func IsManagedLauncher(launcher string) bool {
	return IsNonoLauncher(launcher) || IsOpenShellLauncher(launcher)
}

// PinRuntime preserves legacy recovery while new executions use native nono.
func PinRuntime(ctx context.Context, profile Profile, id string, generation int, deadline, root string) (Profile, error) {
	if !IsNonoLauncher(profile.Launcher) {
		return PinOpenShellRuntime(ctx, profile, id, generation, deadline, root)
	}
	at, err := validateNonoRuntime(id, generation, deadline)
	if err != nil {
		return Profile{}, err
	}
	dir, err := nonoLifecycleDirectory(root, id, profile)
	if err != nil {
		return Profile{}, err
	}
	profile.ExecutionID = id
	profile.ExecutionGeneration = generation
	profile.DeadlineAt = at.UTC().Format(time.RFC3339Nano)
	profile.SupervisorDir = root
	profile.SandboxName = OpenShellSandboxName(id)
	profile.ExecutionLabelValue = OpenShellExecutionLabel(id, generation)
	profile.LifecycleFile = filepath.Join(dir, "nono.json")
	profile.GatewayName = ""
	profile.GatewayEndpoint = ""
	profile.Workspace = ""
	if err := persistOpenShellIdentity(profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func ReadRuntimeLifecycle(profile Profile) (SandboxLifecycle, bool, error) {
	return ReadOpenShellLifecycle(profile)
}

func ReconcileRuntime(ctx context.Context, profile Profile) error {
	if filepath.Base(profile.LifecycleFile) != "nono.json" {
		return ReconcileOpenShellRuntime(ctx, profile)
	}
	lifecycle, exists, err := ReadRuntimeLifecycle(profile)
	if err != nil || !exists {
		return errors.Join(ErrIsolationUnverified, err)
	}
	if lifecycle.SandboxPhase == "Stopped" && lifecycle.SandboxHealth == "stopped" {
		return nil
	}
	registry := os.Getenv("WHIRLWIND_PROCESS_REGISTRY")
	report, err := process.Inspect(registry)
	if err != nil || !report.Clean {
		return errors.Join(ErrIsolationUnverified, err)
	}
	lifecycle.SandboxID = "nono-" + profile.ExecutionLabelValue
	lifecycle.SandboxPhase = "Stopped"
	lifecycle.SandboxHealth = "stopped"
	lifecycle.ActiveProcesses = 0
	return writeNonoLifecycle(profile, lifecycle)
}

func validateNonoRuntime(id string, generation int, deadline string) (time.Time, error) {
	at, err := time.Parse(time.RFC3339Nano, deadline)
	if !validExecutionID(id) || generation < 1 || err != nil || !at.After(time.Now()) {
		return at, fmt.Errorf("%w: invalid nono execution identity or deadline", ErrSandbox)
	}
	_, err = nonoCLI()
	return at, err
}
func nonoLifecycleDirectory(root, id string, profile Profile) (string, error) {
	dir, err := privateLifecycleDirectory(root, id)
	if err == nil && lifecycleDirectoryMounted(dir, profile) {
		err = fmt.Errorf("%w: lifecycle state must remain outside sandbox grants", ErrSandbox)
	}
	return dir, err
}
