package dashboard

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	logRunID    = "run"
	logTextType = "text/plain"
	logJSONType = "application/json"
)

func newRunLogDatabase(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "whirlwind.db")
	database, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Put(context.Background(), "sessions", logRunID, model.Attempt{AttemptID: logRunID}, 0); err != nil {
		t.Fatal(err)
	}
	return database, path
}

func TestRunTailDisplaysWrittenMultilineContent(t *testing.T) {
	database, path := newRunLogDatabase(t)
	if _, err := database.Capture(context.Background(), logRunID, "prompt", logTextType, []byte("First\nTAIL LAST"), 100, false); err != nil {
		t.Fatal(err)
	}
	capture := captureRunTail(t, "TAIL LAST")
	if err := followRunLogs(path, logRunID); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(body), "\n") != 1 || !bytes.Contains(body, []byte(`stype=prompt message="First\nTAIL LAST"`)) {
		t.Fatalf("tail output contains multiline preamble or payload: %q", body)
	}
}
