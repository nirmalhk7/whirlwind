package dashboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

func runTerminalTail(ctx context.Context, path string) (resultErr error) {
	command := exec.CommandContext(ctx, "tail", "-n", "+1", "-f", path)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	terminal := int(os.Stdin.Fd())
	if foreground, err := unix.IoctlGetInt(terminal, unix.TIOCGPGRP); err == nil {
		// Deliver terminal Ctrl-C to tail alone, keeping the dashboard's signal
		// context alive. Restore its foreground group before tview resumes.
		command.SysProcAttr = &syscall.SysProcAttr{Foreground: true, Ctty: terminal}
		// The dashboard is briefly a background process while reclaiming the tty.
		// Suppress the job-control stop signal until ownership has been restored.
		ignored := signal.Ignored(syscall.SIGTTOU)
		signal.Ignore(syscall.SIGTTOU)
		if !ignored {
			defer signal.Reset(syscall.SIGTTOU)
		}
		defer func() {
			if err := unix.IoctlSetPointerInt(terminal, unix.TIOCSPGRP, foreground); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("restore dashboard terminal: %w", err))
			}
		}()
	}
	err := command.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGINT {
			return nil
		}
	}
	return err
}
