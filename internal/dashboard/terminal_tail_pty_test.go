package dashboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTerminalTailKeysInRealPTY(t *testing.T) {
	if path := os.Getenv("WHIRLWIND_TEST_TAIL_PTY_FILE"); path != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runTerminalTail(ctx, path); err != nil {
			t.Fatal(err)
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
