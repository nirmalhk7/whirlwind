//go:build darwin || linux

package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var managedCommands sync.Map

type managedCommand struct {
	record registration
	root   string
	mu     sync.Mutex
	done   bool
}

func setCommandRegistry(cmd *exec.Cmd, root string) {
	environment := []string{}
	for _, entry := range cmd.Env {
		if !strings.HasPrefix(entry, registryEnvironment+"=") {
			environment = append(environment, entry)
		}
	}
	if root != "" {
		environment = append(environment, registryEnvironment+"="+root)
	}
	cmd.Env = environment
}

func forgetCommand(cmd *exec.Cmd) error {
	value, found := managedCommands.Load(cmd)
	if !found {
		return nil
	}
	state := value.(*managedCommand)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.done {
		return nil
	}
	if err := cleanupRegistration(state.record); err != nil {
		return err
	}
	if state.root != "" {
		path := filepath.Join(state.root, state.record.Token+".json")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("%w: cannot remove recovery registration: %w", ErrCleanup, err)
		}
	}
	state.done = true
	managedCommands.Delete(cmd)
	return nil
}

func cleanupRegistration(record registration) error {
	deadline := time.Now().Add(3 * time.Second)
	var last error
	for {
		processes, err := processSnapshot()
		if err == nil {
			live := liveProcesses(record, processes)
			if len(live) == 0 {
				return nil
			}
			last = fmt.Errorf("%d owned processes remain", len(live))
			last = killLiveProcesses(live, last)
		} else {
			last = err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%w: %w", ErrCleanup, last)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func killLiveProcesses(processes []Identity, last error) error {
	for _, identity := range processes {
		if err := killIdentity(identity); err != nil {
			last = err
		}
	}
	return last
}

func killIdentity(identity Identity) error {
	if err := validateKillTarget(identity); err != nil {
		return err
	}
	current, err := kernelIdentity(identity.PID)
	if processIdentityChanged(identity, current, err) {
		return nil
	}
	if err != nil {
		return err
	}
	return signalOwnedProcess(identity.PID)
}

func validateKillTarget(identity Identity) error {
	if identity.PID <= 1 || identity.PID == os.Getpid() {
		return errors.New("refusing unsafe process identity")
	}
	return nil
}

func processIdentityChanged(expected, current Identity, err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) || (err == nil && (current.Started != expected.Started || current.Zombie))
}

func signalOwnedProcess(pid int) error {
	err := syscall.Kill(pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
