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
	"strconv"
	"strings"
	"syscall"
	"time"
)

type registration struct {
	PID   int    `json:"pid"`
	Token string `json:"token"`
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

// Command creates a cancellable process-group command with a unique environment token for restart recovery.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	Configure(cmd)
	var token [16]byte
	_, _ = rand.Read(token[:])
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "WHIRLWIND_PROCESS_TOKEN=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "WHIRLWIND_PROCESS_TOKEN="+hex.EncodeToString(token[:]))
	return cmd
}

// token extracts the recovery token from a command environment.
func token(cmd *exec.Cmd) string {
	for _, v := range cmd.Env {
		if strings.HasPrefix(v, "WHIRLWIND_PROCESS_TOKEN=") {
			return strings.TrimPrefix(v, "WHIRLWIND_PROCESS_TOKEN=")
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
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(root, v.Token+".json"))
}

// Start registers a token before launch and its PID afterward; registration failure cancels and waits for the child.
func Start(cmd *exec.Cmd) error {
	root := os.Getenv("WHIRLWIND_PROCESS_REGISTRY")
	if root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			return err
		}
		if err := writeRegistration(root, registration{Token: token(cmd)}); err != nil {
			return err
		}
	}
	if err := cmd.Start(); err != nil {
		if root != "" {
			_ = os.Remove(filepath.Join(root, token(cmd)+".json"))
		}
		return err
	}
	if root != "" {
		if err := writeRegistration(root, registration{PID: cmd.Process.Pid, Token: token(cmd)}); err != nil {
			_ = cmd.Cancel()
			_ = cmd.Wait()
			return err
		}
	}
	return nil
}

// Forget kills any remaining descendants and removes the command registration after execution.
func Forget(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if root := os.Getenv("WHIRLWIND_PROCESS_REGISTRY"); root != "" {
		_ = os.Remove(filepath.Join(root, token(cmd)+".json"))
	}
}

// Run starts a registered command, waits for its exit, and cleans up its process group and registration.
func Run(cmd *exec.Cmd) error {
	if e := Start(cmd); e != nil {
		return e
	}
	defer Forget(cmd)
	return cmd.Wait()
}

// Reap finds process groups carrying registered tokens, kills them, and removes their orphan registrations before restart.
func Reap(root string) error {
	files, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, file.Name()))
		if err != nil {
			return err
		}
		var v registration
		if json.Unmarshal(b, &v) != nil || v.PID < 0 || v.PID == 1 || len(v.Token) != 32 {
			return errors.New("invalid orphan process registration")
		}
		output, err := exec.Command("ps", "eww", "-A", "-o", "pid=", "-o", "pgid=", "-o", "command=").Output()
		if err != nil {
			return fmt.Errorf("cannot reconcile process registry: %w", err)
		}
		groups := map[int]bool{}
		for _, line := range strings.Split(string(output), "\n") {
			if !strings.Contains(line, "WHIRLWIND_PROCESS_TOKEN="+v.Token) {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			group, err := strconv.Atoi(fields[1])
			if err != nil || group <= 1 {
				return errors.New("invalid orphan process group")
			}
			groups[group] = true
		}
		for group := range groups {
			if err = syscall.Kill(-group, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
				return err
			}
		}
		if err = os.Remove(filepath.Join(root, file.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Lock takes a nonblocking exclusive file lock to prevent multiple servers from owning one data directory.
func Lock(path string) (*os.File, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
