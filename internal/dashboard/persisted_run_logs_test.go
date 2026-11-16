package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunViewerTailsPersistedBytesWithoutFormatting(t *testing.T) {
	database, databasePath := newRunLogDatabase(t)
	if _, err := database.Capture(context.Background(), logRunID, "output", logTextType, []byte("VIEWER STOP"), 100, false); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(filepath.Dir(databasePath), "run-logs")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	want := "2026-11-09T16:57:58.602701Z run=run seq=1 stype=output message=\"VIEWER STOP\"\n"
	if err := os.WriteFile(filepath.Join(directory, logRunID+".log"), []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	capture := captureRunTail(t, "VIEWER STOP")
	if err := followRunLogs(databasePath, logRunID); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(capture)
	if err != nil || string(body) != want {
		t.Fatalf("viewer reformatted stored bytes: %q %v", body, err)
	}
}
