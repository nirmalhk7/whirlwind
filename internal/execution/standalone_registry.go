package execution

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

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

// MarkStandaloneCleanupPending retains the locator when isolation verification fails.
func MarkStandaloneCleanupPending(profile Profile) error {
	owner, err := process.IdentityAt(os.Getpid())
	if err != nil || owner.Started == "" {
		return fmt.Errorf("%w: cannot verify standalone supervisor identity", ErrIsolationUnverified)
	}
	expected := standaloneLocator(profile)
	expected.OwnerPID, expected.OwnerStarted = owner.PID, owner.Started
	return updateStandaloneRegistry(profile.SupervisorDir, func(registry *standaloneRegistry) error {
		for index := range registry.Executions {
			if registry.Executions[index].ExecutionID == profile.ExecutionID {
				if !sameStandaloneAuthority(registry.Executions[index], expected) || registry.Executions[index].OwnerPID != expected.OwnerPID || registry.Executions[index].OwnerStarted != expected.OwnerStarted {
					return fmt.Errorf("%w: standalone runtime registry authority changed", ErrIsolationUnverified)
				}
				registry.Executions[index].State = "cleanup_pending"
				return nil
			}
		}
		return errors.New("standalone runtime registry entry is missing")
	})
}

// CompleteStandaloneRuntime removes only a matching locator after sandbox absence is verified.
func CompleteStandaloneRuntime(profile Profile) error {
	owner, err := process.IdentityAt(os.Getpid())
	if err != nil || owner.Started == "" {
		return fmt.Errorf("%w: cannot verify standalone supervisor identity", ErrIsolationUnverified)
	}
	expected := standaloneLocator(profile)
	expected.OwnerPID, expected.OwnerStarted = owner.PID, owner.Started
	return updateStandaloneRegistry(profile.SupervisorDir, func(registry *standaloneRegistry) error {
		for index, existing := range registry.Executions {
			if existing.ExecutionID != profile.ExecutionID {
				continue
			}
			if !sameStandaloneAuthority(existing, expected) || existing.OwnerPID != expected.OwnerPID || existing.OwnerStarted != expected.OwnerStarted {
				return fmt.Errorf("%w: standalone runtime registry authority changed", ErrIsolationUnverified)
			}
			registry.Executions = append(registry.Executions[:index], registry.Executions[index+1:]...)
			return nil
		}
		return errors.New("standalone runtime registry entry is missing")
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

// StandaloneRuntimeProfile validates a registered locator and restores its exact pinned authority.
func StandaloneRuntimeProfile(root string, locator StandaloneRuntimeLocator) (Profile, error) {
	deadline, deadlineErr := time.Parse(time.RFC3339Nano, locator.DeadlineAt)
	expectedLifecycle := filepath.Join(root, locator.ExecutionID, "openshell.json")
	if !filepath.IsAbs(root) || !validExecutionID(locator.ExecutionID) || locator.Generation < 1 ||
		locator.SandboxName != OpenShellSandboxName(locator.ExecutionID) || locator.RuntimeKind != "openshell" ||
		!filepath.IsAbs(locator.Launcher) || !IsOpenShellLauncher(locator.Launcher) || !filepath.IsAbs(locator.CWD) || filepath.Clean(locator.CWD) == string(filepath.Separator) ||
		locator.DeadlineAt == "" || deadlineErr != nil || deadline.IsZero() || locator.OwnerPID <= 1 || locator.OwnerStarted == "" ||
		locator.LifecycleFile != expectedLifecycle || locator.GatewayName == "" || locator.GatewayEndpoint == "" || locator.Workspace == "" ||
		locator.State != "active" && locator.State != "cleanup_pending" {
		return Profile{}, fmt.Errorf("%w: registered standalone runtime identity is incomplete or invalid", ErrIsolationUnverified)
	}
	return Profile{ExecutionID: locator.ExecutionID, ExecutionGeneration: locator.Generation, SandboxName: locator.SandboxName, SandboxID: locator.SandboxID,
		LifecycleFile: locator.LifecycleFile, GatewayName: locator.GatewayName, GatewayEndpoint: locator.GatewayEndpoint,
		Workspace: locator.Workspace, DeadlineAt: locator.DeadlineAt, CWD: locator.CWD, Launcher: locator.Launcher,
		SupervisorDir: root, ExecutionLabelValue: OpenShellExecutionLabel(locator.ExecutionID, locator.Generation), StandaloneRuntime: true}, nil
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
	return StandaloneRuntimeLocator{ExecutionID: profile.ExecutionID, LifecycleFile: profile.LifecycleFile, SandboxName: profile.SandboxName,
		GatewayName: profile.GatewayName, GatewayEndpoint: profile.GatewayEndpoint, Workspace: profile.Workspace,
		Launcher: profile.Launcher, RuntimeKind: "openshell", Generation: profile.ExecutionGeneration,
		CWD: profile.CWD, DeadlineAt: profile.DeadlineAt, State: "active"}
}

func sameStandaloneAuthority(left, right StandaloneRuntimeLocator) bool {
	left.SandboxID, right.SandboxID = "", ""
	left.OwnerPID, right.OwnerPID = 0, 0
	left.OwnerStarted, right.OwnerStarted = "", ""
	left.State, right.State = "", ""
	return left == right
}

func updateStandaloneRegistry(root string, update func(*standaloneRegistry) error) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("%w: standalone runtime registry root must be absolute", ErrIsolationUnverified)
	}
	standaloneRegistryMutex.Lock()
	defer standaloneRegistryMutex.Unlock()
	lock, err := lockStandaloneRegistry(root)
	if err != nil {
		return err
	}
	defer unlockStandaloneRegistry(lock)
	registry, err := readStandaloneRegistry(root)
	if err != nil {
		return err
	}
	if err := update(&registry); err != nil {
		return err
	}
	sort.Slice(registry.Executions, func(i, j int) bool { return registry.Executions[i].ExecutionID < registry.Executions[j].ExecutionID })
	return writeStandaloneRegistry(root, registry)
}

func lockStandaloneRegistry(root string) (*os.File, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("%w: cannot create standalone runtime registry", ErrIsolationUnverified)
	}
	if err := os.Chmod(root, 0700); err != nil {
		return nil, fmt.Errorf("%w: cannot protect standalone runtime registry", ErrIsolationUnverified)
	}
	file, err := os.OpenFile(filepath.Join(root, ".active.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot lock standalone runtime registry", ErrIsolationUnverified)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: cannot lock standalone runtime registry", ErrIsolationUnverified)
	}
	return file, nil
}

func unlockStandaloneRegistry(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func readStandaloneRegistry(root string) (standaloneRegistry, error) {
	data, err := os.ReadFile(StandaloneRegistryPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return standaloneRegistry{Version: standaloneRegistryVersion, Executions: []StandaloneRuntimeLocator{}}, nil
	}
	if err != nil {
		return standaloneRegistry{}, fmt.Errorf("%w: cannot read standalone runtime registry", ErrIsolationUnverified)
	}
	var registry standaloneRegistry
	if err := json.Unmarshal(data, &registry); err != nil || registry.Version != standaloneRegistryVersion || registry.Executions == nil {
		return standaloneRegistry{}, fmt.Errorf("%w: standalone runtime registry is invalid", ErrIsolationUnverified)
	}
	return registry, nil
}

func writeStandaloneRegistry(root string, registry standaloneRegistry) error {
	data, err := json.Marshal(registry)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(root, ".active-*.tmp")
	if err != nil {
		return fmt.Errorf("%w: cannot write standalone runtime registry", ErrIsolationUnverified)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if err = temporary.Chmod(0600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	err = errors.Join(err, temporary.Close())
	if err != nil {
		return fmt.Errorf("%w: cannot sync standalone runtime registry", ErrIsolationUnverified)
	}
	if err := os.Rename(name, StandaloneRegistryPath(root)); err != nil {
		return fmt.Errorf("%w: cannot publish standalone runtime registry", ErrIsolationUnverified)
	}
	directory, err := os.Open(root)
	if err != nil {
		return fmt.Errorf("%w: cannot sync standalone runtime registry directory", ErrIsolationUnverified)
	}
	err = errors.Join(directory.Sync(), directory.Close())
	if err != nil {
		return fmt.Errorf("%w: cannot sync standalone runtime registry directory", ErrIsolationUnverified)
	}
	return nil
}
