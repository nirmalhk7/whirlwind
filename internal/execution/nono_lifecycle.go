package execution

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
	"whirlwind/internal/process"
)

var nonoLifecycleMu sync.Mutex

type nonoLifecycleOwner struct {
	profile Profile
	started bool
}

func attachNonoLifecycle(cmd *exec.Cmd, profile Profile) error {
	if !IsNonoLauncher(profile.Launcher) || profile.LifecycleFile == "" {
		return nil
	}
	owner := &nonoLifecycleOwner{profile: profile}
	if err := process.OnStart(cmd, owner.start); err != nil {
		return err
	}
	return process.OnCleanup(cmd, owner.cleanup)
}
func (owner *nonoLifecycleOwner) start() error {
	nonoLifecycleMu.Lock()
	defer nonoLifecycleMu.Unlock()
	lifecycle, exists, err := ReadRuntimeLifecycle(owner.profile)
	if err != nil || !exists {
		return errors.Join(ErrIsolationUnverified, err)
	}
	lifecycle.SandboxID = "nono-" + owner.profile.ExecutionLabelValue
	lifecycle.SandboxPhase = "Ready"
	lifecycle.SandboxHealth = "healthy"
	lifecycle.ActiveProcesses++
	if err := writeNonoLifecycle(owner.profile, lifecycle); err != nil {
		return err
	}
	owner.started = true
	return nil
}
func (owner *nonoLifecycleOwner) cleanup() {
	nonoLifecycleMu.Lock()
	defer nonoLifecycleMu.Unlock()
	if !owner.started {
		return
	}
	lifecycle, exists, err := ReadRuntimeLifecycle(owner.profile)
	if err != nil || !exists {
		return
	}
	lifecycle.ActiveProcesses--
	if lifecycle.ActiveProcesses == 0 {
		lifecycle.SandboxPhase = "Stopped"
		lifecycle.SandboxHealth = "stopped"
	}
	_ = writeNonoLifecycle(owner.profile, lifecycle)
}

func writeNonoLifecycle(profile Profile, lifecycle SandboxLifecycle) error {
	lifecycle.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(lifecycle)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(profile.LifecycleFile), ".nono-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), profile.LifecycleFile)
}
