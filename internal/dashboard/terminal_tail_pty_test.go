package dashboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestTerminalTailKeysInRealPTY(t *testing.T) {
	if path := os.Getenv("WHIRLWIND_TEST_TAIL_PTY_FILE"); path != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		fd := int(os.Stdin.Fd())
		before, err := unix.IoctlGetTermios(fd, tailGetTermios)
		if err != nil {
			t.Fatal(err)
		}
		if err := runTerminalTail(ctx, path); err != nil {
			t.Fatal(err)
		}
		after, err := unix.IoctlGetTermios(fd, tailGetTermios)
		if err != nil {
			t.Fatal(err)
		}
		if *after != *before {
			t.Fatalf("log viewer did not restore terminal settings: before=%+v after=%+v", before, after)
		}
		fmt.Println("TAIL_VIEW_RETURNED")
		return
	}
	path := filepath.Join(t.TempDir(), "run.log")
	if err := os.WriteFile(path, []byte("SAVED_LOG_LINE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "testdata/terminal-tail-pty.py", os.Args[0])
	command.Env = append(os.Environ(), "WHIRLWIND_TEST_TAIL_PTY_FILE="+path)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("real PTY run-log test: %v\n%s", err, out)
	}
}
