//go:build darwin || linux

package process

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

const processTokenPrefix = "WHIRLWIND_PROCESS_TOKEN="

var commandCleanupMu sync.Mutex
var commandCleanups = make(map[*exec.Cmd][]func())
var commandStarts = make(map[*exec.Cmd][]func() error)

type registration struct {
	PID     int    `json:"pid"`
	Token   string `json:"token"`
	Started string `json:"started,omitempty"`
}

// Configure isolates a child in its own process group so cancellation kills its descendants and bounds pipe cleanup.
func Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
}

// OnCleanup releases process-scoped resources only after Forget confirms that
// the owned process group has drained. Failed cleanup leaves callbacks pending.
func OnCleanup(cmd *exec.Cmd, callback func()) error {
	if cmd == nil || callback == nil {
		return errors.New("process cleanup callback requires a command and function")
	}
	if cmd.Process != nil {
		return errors.New("process cleanup callback must be registered before start")
	}
	commandCleanupMu.Lock()
	commandCleanups[cmd] = append(commandCleanups[cmd], callback)
	commandCleanupMu.Unlock()
	return nil
}

// OnStart promotes resources after the managed child starts; a failure stops
// the child and uses its normal verified cleanup path.
func OnStart(cmd *exec.Cmd, callback func() error) error {
	if cmd == nil || callback == nil {
		return errors.New("process start callback requires a command and function")
	}
	if cmd.Process != nil {
		return errors.New("process start callback must be registered before start")
	}
	commandCleanupMu.Lock()
	commandStarts[cmd] = append(commandStarts[cmd], callback)
	commandCleanupMu.Unlock()
	return nil
}

func runStartCallbacks(cmd *exec.Cmd) error {
	commandCleanupMu.Lock()
	callbacks := commandStarts[cmd]
	delete(commandStarts, cmd)
	commandCleanupMu.Unlock()
	for _, callback := range callbacks {
		if err := callback(); err != nil {
			return err
		}
	}
	return nil
}

func runCleanupCallbacks(cmd *exec.Cmd) {
	commandCleanupMu.Lock()
	callbacks := commandCleanups[cmd]
	delete(commandCleanups, cmd)
	delete(commandStarts, cmd)
	commandCleanupMu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

func hasCleanupCallbacks(cmd *exec.Cmd) bool {
	commandCleanupMu.Lock()
	defer commandCleanupMu.Unlock()
	return len(commandCleanups[cmd]) > 0
}

// Command creates a cancellable process-group command with a unique environment token for restart recovery.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	Configure(cmd)
	var token [16]byte
	_, _ = rand.Read(token[:])
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, processTokenPrefix) {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, processTokenPrefix+hex.EncodeToString(token[:]))
	return cmd
}

// token extracts the recovery token from a command environment.
func token(cmd *exec.Cmd) string {
	for _, v := range cmd.Env {
		if strings.HasPrefix(v, processTokenPrefix) {
			return strings.TrimPrefix(v, processTokenPrefix)
		}
	}
	return ""
}

// writeRegistration writes and syncs a temporary process registration, then atomically renames it into the registry.
func writeRegistration(root string, v registration) error {
	b, _ := json.Marshal(v)
	f, err := os.CreateTemp(root, ".registration-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(root, v.Token+".json"))
}

// Start registers a token before launch and its PID afterward; registration failure cancels and waits for the child.
func Start(cmd *exec.Cmd) error {
	if !validToken(token(cmd)) {
		runCleanupCallbacks(cmd)
		return errors.New("managed process requires an ownership token")
	}
	root := os.Getenv(registryEnvironment)
	setCommandRegistry(cmd, root)
	state, err := prepareCommandRegistration(cmd, root)
	if err != nil {
		runCleanupCallbacks(cmd)
		return err
	}
	managedCommands.Store(cmd, state)
	if err := startRegisteredCommand(cmd, state, root); err != nil {
		return err
	}
	if err := runStartCallbacks(cmd); err != nil {
		_ = cmd.Cancel()
		return errors.Join(err, cmd.Wait(), Forget(cmd))
	}
	return nil
}

func prepareCommandRegistration(cmd *exec.Cmd, root string) (*managedCommand, error) {
	state := &managedCommand{record: registration{Token: token(cmd)}, root: root}
	if root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			return nil, err
		}
		if err := writeRegistration(root, registration{Token: token(cmd)}); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func startRegisteredCommand(cmd *exec.Cmd, state *managedCommand, root string) error {
	if err := cmd.Start(); err != nil {
		managedCommands.Delete(cmd)
		if root != "" {
			_ = os.Remove(filepath.Join(root, token(cmd)+".json"))
		}
		runCleanupCallbacks(cmd)
		return err
	}
	state.record.PID = cmd.Process.Pid
	identity, err := kernelIdentity(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Cancel()
		return errors.Join(err, cmd.Wait(), Forget(cmd))
	}
	state.record.Started = identity.Started
	if root != "" {
		if err := writeRegistration(root, state.record); err != nil {
			_ = cmd.Cancel()
			return errors.Join(err, cmd.Wait(), Forget(cmd))
		}
	}
	return nil
}

// Forget kills any remaining descendants and removes the command registration after execution.
func Forget(cmd *exec.Cmd) error {
	_, managed := managedCommands.Load(cmd)
	err := forgetCommand(cmd)
	if err != nil {
		return err
	}
	if cmd != nil && cmd.Process != nil && !managed && hasCleanupCallbacks(cmd) {
		return fmt.Errorf("%w: sensitive process was not started through process.Start", ErrCleanup)
	}
	runCleanupCallbacks(cmd)
	return nil
}

// Run starts a registered command, waits for its exit, and cleans up its process group and registration.
func Run(cmd *exec.Cmd) error {
	if e := Start(cmd); e != nil {
		return e
	}
	return Wait(cmd)
}

// WaitForDrain reaps the child and verifies process-group cleanup, but leaves
// registered callbacks pending so callers can drain their output first.
func WaitForDrain(cmd *exec.Cmd) (func(), error) {
	waitErr := cmd.Wait()
	_, managed := managedCommands.Load(cmd)
	cleanupErr := forgetCommand(cmd)
	if cleanupErr != nil {
		return nil, errors.Join(waitErr, cleanupErr)
	}
	if cmd != nil && cmd.Process != nil && !managed && hasCleanupCallbacks(cmd) {
		return nil, errors.Join(waitErr, fmt.Errorf("%w: sensitive process was not started through process.Start", ErrCleanup))
	}
	var once sync.Once
	return func() { once.Do(func() { runCleanupCallbacks(cmd) }) }, waitErr
}

// Wait reaps the direct child, verifies descendant cleanup, and then releases cleanup callbacks.
func Wait(cmd *exec.Cmd) error {
	release, err := WaitForDrain(cmd)
	if release != nil {
		release()
	}
	return err
}

// Lock takes a nonblocking exclusive file lock to prevent multiple servers from owning one data directory.
func Lock(path string) (*os.File, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		_ = f.Close()
		return nil, e
	}
	return f, nil
}
