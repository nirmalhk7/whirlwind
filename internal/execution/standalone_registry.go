package execution

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"whirlwind/internal/process"
)

const standaloneRegistryVersion = 1

type StandaloneRuntimeLocator struct {
	ExecutionID     string `json:"execution_id"`
	SandboxID       string `json:"sandbox_id,omitempty"`
	LifecycleFile   string `json:"lifecycle_file"`
	SandboxName     string `json:"sandbox_name"`
	GatewayName     string `json:"gateway_name"`
	GatewayEndpoint string `json:"gateway_endpoint"`
	Workspace       string `json:"workspace"`
	Launcher        string `json:"launcher"`
	RuntimeKind     string `json:"runtime_kind"`
	CWD             string `json:"cwd"`
	DeadlineAt      string `json:"deadline_at"`
	OwnerStarted    string `json:"owner_started"`
	OwnerPID        int    `json:"owner_pid"`
	Generation      int    `json:"generation"`
	State           string `json:"state"`
}

type standaloneRegistry struct {
	Version    int                        `json:"version"`
	Executions []StandaloneRuntimeLocator `json:"executions"`
}

var standaloneRegistryMutex sync.Mutex

func StandaloneRegistryPath(root string) string { return filepath.Join(root, "active.json") }

// RegisterStandaloneRuntime records the exact runtime authority before launch.
func RegisterStandaloneRuntime(profile Profile) error {
	owner, err := process.IdentityAt(os.Getpid())
	if err != nil || owner.Started == "" {
		return fmt.Errorf("%w: cannot record standalone supervisor identity", ErrIsolationUnverified)
	}
	locator := standaloneLocator(profile)
	locator.OwnerPID, locator.OwnerStarted = owner.PID, owner.Started
	return updateStandaloneRegistry(profile.SupervisorDir, func(registry *standaloneRegistry) error {
		for index, existing := range registry.Executions {
			if existing.ExecutionID == locator.ExecutionID {
				if !sameStandaloneAuthority(existing, locator) || existing.OwnerPID != locator.OwnerPID || existing.OwnerStarted != locator.OwnerStarted {
					return fmt.Errorf("%w: standalone runtime identity conflicts with its registry entry", ErrIsolationUnverified)
				}
				if existing.State == "cleanup_pending" {
					return fmt.Errorf("%w: standalone runtime still requires cleanup", ErrIsolationUnverified)
				}
				locator.SandboxID = existing.SandboxID
				registry.Executions[index] = locator
				return nil
			}
		}
		registry.Executions = append(registry.Executions, locator)
		return nil
	})
}

// StandaloneRuntimeOwnerIsCurrent verifies both PID and kernel start time against this supervisor.
func StandaloneRuntimeOwnerIsCurrent(locator StandaloneRuntimeLocator) (bool, error) {
	if locator.OwnerPID <= 1 || locator.OwnerStarted == "" {
		return false, fmt.Errorf("%w: standalone runtime owner identity is missing", ErrIsolationUnverified)
	}
	current, err := process.IdentityAt(os.Getpid())
	if err != nil {
		return false, fmt.Errorf("%w: cannot verify current supervisor identity", ErrIsolationUnverified)
	}
	return locator.OwnerPID == current.PID && locator.OwnerStarted == current.Started, nil
}

// PinStandaloneSandboxID durably binds a discovered sandbox before cleanup or replacement begins.
func PinStandaloneSandboxID(profile Profile, sandboxID string) error {
	if !profile.StandaloneRuntime || sandboxID == "" {
		return fmt.Errorf("%w: standalone sandbox resource ID is missing", ErrIsolationUnverified)
	}
	expected := standaloneLocator(profile)
	return updateStandaloneRegistry(profile.SupervisorDir, func(registry *standaloneRegistry) error {
		for index := range registry.Executions {
			existing := registry.Executions[index]
			if existing.ExecutionID != profile.ExecutionID {
				continue
			}
			if !sameStandaloneAuthority(existing, expected) || existing.State != "active" && existing.State != "cleanup_pending" {
				return fmt.Errorf("%w: standalone runtime registry authority changed before sandbox ID pin", ErrIsolationUnverified)
			}
			if existing.SandboxID != "" && existing.SandboxID != sandboxID {
				return fmt.Errorf("%w: discovered sandbox ID conflicts with durable standalone identity", ErrIsolationUnverified)
			}
			registry.Executions[index].SandboxID = sandboxID
			return nil
		}
		return fmt.Errorf("%w: standalone runtime registry entry is missing", ErrIsolationUnverified)
	})
}

// CompleteStandaloneLocator removes only the exact record read by recovery after sandbox absence is verified.
func CompleteStandaloneLocator(root string, expected StandaloneRuntimeLocator) error {
	return updateStandaloneRegistry(root, func(registry *standaloneRegistry) error {
		for index, existing := range registry.Executions {
			if existing.ExecutionID != expected.ExecutionID {
				continue
			}
			if existing != expected {
				return fmt.Errorf("%w: standalone runtime registry authority changed during recovery", ErrIsolationUnverified)
			}
			registry.Executions = append(registry.Executions[:index], registry.Executions[index+1:]...)
			return nil
		}
		return errors.New("standalone runtime registry entry is missing")
	})
}

// ReadStandaloneRuntimes returns registered locators only; callers must not sweep unregistered sidecars.
func ReadStandaloneRuntimes(root string) ([]StandaloneRuntimeLocator, error) {
	standaloneRegistryMutex.Lock()
	defer standaloneRegistryMutex.Unlock()
	lock, err := lockStandaloneRegistry(root)
	if err != nil {
		return nil, err
	}
	defer unlockStandaloneRegistry(lock)
	registry, err := readStandaloneRegistry(root)
	if err != nil {
		return nil, err
	}
	return append([]StandaloneRuntimeLocator{}, registry.Executions...), nil
}

func standaloneLocator(profile Profile) StandaloneRuntimeLocator {
	kind := "openshell"
	if IsNonoLauncher(profile.Launcher) {
		kind = "nono"
	}
	return StandaloneRuntimeLocator{ExecutionID: profile.ExecutionID, LifecycleFile: profile.LifecycleFile, SandboxName: profile.SandboxName,
		GatewayName: profile.GatewayName, GatewayEndpoint: profile.GatewayEndpoint, Workspace: profile.Workspace,
		Launcher: profile.Launcher, RuntimeKind: kind, Generation: profile.ExecutionGeneration,
		CWD: profile.CWD, DeadlineAt: profile.DeadlineAt, State: "active"}
}

func sameStandaloneAuthority(left, right StandaloneRuntimeLocator) bool {
	left.SandboxID, right.SandboxID = "", ""
	left.OwnerPID, right.OwnerPID = 0, 0
	left.OwnerStarted, right.OwnerStarted = "", ""
	left.State, right.State = "", ""
	return left == right
}
