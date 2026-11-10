package execution

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

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
	return applyStandaloneRegistryUpdate(root, &registry, update)
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
	return publishStandaloneRegistry(root, name)
}

func publishStandaloneRegistry(root, name string) error {
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

func applyStandaloneRegistryUpdate(root string, registry *standaloneRegistry, update func(*standaloneRegistry) error) error {
	if err := update(registry); err != nil {
		return err
	}
	sort.Slice(registry.Executions, func(i, j int) bool { return registry.Executions[i].ExecutionID < registry.Executions[j].ExecutionID })
	return writeStandaloneRegistry(root, *registry)
}
