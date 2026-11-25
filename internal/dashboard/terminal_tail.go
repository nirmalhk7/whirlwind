package dashboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	tailInputPoll   = 50 * time.Millisecond
	tailEscapeGrace = 75 * time.Millisecond
)

func runTerminalTail(ctx context.Context, path string) (resultErr error) {
	terminal := int(os.Stdin.Fd())
	saved, err := unix.IoctlGetTermios(terminal, tailGetTermios)
	if errors.Is(err, unix.ENOTTY) {
		return execTerminalTail(ctx, path)
	}
	if err != nil {
		return fmt.Errorf("read log viewer terminal: %w", err)
	}
	input := *saved
	// Read keys immediately without echo or terminal signals. Keep the
	// original output processing, so logs still render normal line endings.
	input.Lflag &^= unix.ECHO | unix.ICANON | unix.ISIG | unix.IEXTEN
	input.Iflag &^= unix.IXON
	input.Cc[unix.VMIN], input.Cc[unix.VTIME] = 0, 0
	if err := unix.IoctlSetTermios(terminal, tailSetTermios, &input); err != nil {
		return fmt.Errorf("configure log viewer terminal: %w", err)
	}
	defer func() {
		if err := unix.IoctlSetTermios(terminal, tailSetTermios, saved); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("restore dashboard terminal: %w", err))
		}
	}()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return followTerminalTail(ctx, path, terminal)
}

func followTerminalTail(ctx context.Context, path string, terminal int) error {
	if _, err := fmt.Fprintln(os.Stdout, "Run logs · Esc or Ctrl-C to return to the dashboard"); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		closeKey bool
		err      error
	}
	keys := make(chan result, 1)
	go func() {
		closeKey, err := waitForTailClose(ctx, terminal)
		keys <- result{closeKey, err}
		cancel()
	}()
	runErr := execTerminalTail(ctx, path)
	cancel()
	key := <-keys
	if key.err != nil {
		return fmt.Errorf("read log viewer keys: %w", key.err)
	}
	if key.closeKey && (errors.Is(runErr, context.Canceled) || tailExitSignal(runErr) == syscall.SIGKILL) {
		return nil
	}
	return runErr
}

func execTerminalTail(ctx context.Context, path string) error {
	command := exec.CommandContext(ctx, "tail", "-n", "+1", "-f", path)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	err := command.Run()
	if tailExitSignal(err) == syscall.SIGINT {
		return nil
	}
	return err
}

func tailExitSignal(err error) syscall.Signal {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return status.Signal()
		}
	}
	return 0
}

// A lone Esc closes the viewer. Do not mistake an ANSI sequence split across
// reads (such as an arrow key) for Esc; allow its next byte a short grace period.
func waitForTailClose(ctx context.Context, terminal int) (bool, error) {
	poll := []unix.PollFd{{Fd: int32(terminal), Events: unix.POLLIN}}
	var escapeUntil time.Time
	var buffer [64]byte
	for ctx.Err() == nil {
		timeout, expired := tailPollTimeout(escapeUntil)
		if expired {
			return true, nil
		}
		keys, err := readTailKeys(terminal, poll, timeout, buffer[:])
		if err != nil {
			return false, err
		}
		if consumeTailKeys(keys, &escapeUntil) {
			return true, nil
		}
	}
	return false, nil
}

func tailPollTimeout(escapeUntil time.Time) (time.Duration, bool) {
	if escapeUntil.IsZero() {
		return tailInputPoll, false
	}
	remaining := time.Until(escapeUntil)
	return min(tailInputPoll, remaining), remaining <= 0
}

func readTailKeys(terminal int, poll []unix.PollFd, timeout time.Duration, buffer []byte) ([]byte, error) {
	ready, err := unix.Poll(poll, int((timeout+time.Millisecond-1)/time.Millisecond))
	if errors.Is(err, unix.EINTR) {
		return nil, nil
	}
	if err != nil || ready == 0 {
		return nil, err
	}
	if poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
		return nil, io.ErrClosedPipe
	}
	count, err := unix.Read(terminal, buffer)
	if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return buffer[:count], nil
}

func consumeTailKeys(keys []byte, escapeUntil *time.Time) bool {
	for _, key := range keys {
		switch key {
		case 0x03:
			return true
		case 0x1b:
			*escapeUntil = time.Now().Add(tailEscapeGrace)
		default:
			*escapeUntil = time.Time{}
		}
	}
	return false
}
