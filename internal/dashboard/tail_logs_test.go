package dashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	tailInitialMarker = "INITIAL UNIQUE"
	tailPlainTextType = "text/plain"
	tailLastMarker    = "LIVE LAST"
)

// Run the actual tail binary; capture terminal output and stop after a fixture
// marker, so integration tests need no interactive keyboard or test-only API.
func captureRunTail(t *testing.T, stop string) string {
	t.Helper()
	realTail, err := exec.LookPath("tail")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	capture := filepath.Join(directory, "output")
	t.Setenv("REAL_RUN_TAIL", realTail)
	t.Setenv("RUN_TAIL_OUTPUT", capture)
	t.Setenv("RUN_TAIL_STOP", stop)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/usr/bin/env python3
import os,sys,subprocess,signal
def expired(*_): raise TimeoutError('tail fixture timed out')
signal.signal(signal.SIGALRM,expired)
signal.alarm(10)
p=subprocess.Popen([os.environ['REAL_RUN_TAIL']]+sys.argv[1:],stdout=subprocess.PIPE)
try:
 with open(os.environ['RUN_TAIL_OUTPUT'],'wb',buffering=0) as output:
  for line in p.stdout:
   output.write(line)
   if os.environ['RUN_TAIL_STOP'].encode() in line: break
finally:
 p.terminate()
 p.wait()
`
	if err := os.WriteFile(filepath.Join(directory, "tail"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return capture
}

func TestTerminalRunTailFollowsNewEventsWithoutDuplicates(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("WHIRLWIND_DATA_DIR", directory)
	database, err := store.Open(filepath.Join(directory, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	ctx := context.Background()
	attempt := model.Attempt{AttemptID: "run", Role: "ideate", Status: "running"}
	if err := database.Put(ctx, "sessions", "run", attempt, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Capture(ctx, "run", "prompt", tailPlainTextType, []byte(tailInitialMarker), 100, false); err != nil {
		t.Fatal(err)
	}
	capture := captureRunTail(t, tailLastMarker)
	d := New()
	dashboardScreen(t, d)
	d.Present(control.Snapshot{"sessions": {record("run", attempt)}})
	appended := make(chan error, 1)
	go func() {
		if err := waitForRunTailMarker(capture); err != nil {
			appended <- err
			return
		}
		_, err := database.Capture(ctx, "run", "output", tailPlainTextType, []byte(tailLastMarker), 100, false)
		appended <- err
	}()
	d.showRunLogs(0)
	if err := <-appended; err != nil {
		t.Fatalf("initial terminal logs never became available: %v", err)
	}
	body, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Count(text, tailInitialMarker) != 1 || strings.Count(text, tailLastMarker) != 1 {
		t.Fatalf("tail missed or duplicated events: %q", text)
	}
	if d.modal != nil {
		t.Fatal("run logs still use a tview modal")
	}
}

func prepareRunTailFixture(t *testing.T, snapshot control.Snapshot) string {
	t.Helper()
	directory := t.TempDir()
	t.Setenv("WHIRLWIND_DATA_DIR", directory)
	database, err := store.Open(filepath.Join(directory, "whirlwind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	ctx := context.Background()
	for _, kind := range []string{"projects", "execution_plans", "questions"} {
		for _, record := range snapshot[kind] {
			if err := database.Put(ctx, kind, record.ID, record.Data, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, record := range snapshot["sessions"] {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil {
			t.Fatal(err)
		}
		if err := database.Put(ctx, "sessions", record.ID, attempt, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Capture(ctx, record.ID, "output", tailPlainTextType, []byte("TAIL FIXTURE END"), 100, false); err != nil {
			t.Fatal(err)
		}
	}
	return captureRunTail(t, "TAIL FIXTURE END")
}

func waitForRunTailMarker(capture string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		body, _ := os.ReadFile(capture)
		if strings.Contains(string(body), tailInitialMarker) {
			return nil
		}
		if time.Now().After(deadline) {
			return os.ErrDeadlineExceeded
		}
		time.Sleep(10 * time.Millisecond)
	}
}
