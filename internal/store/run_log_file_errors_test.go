package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunLogPathReportsFileInspectionError(t *testing.T) {
	s, _, path := persistedLogFixture(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(path), path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunLogPath(context.Background(), persistedRunID); err == nil {
		t.Fatal("run log lookup hid file inspection failure")
	}
}

func TestRunLogWriteFailureKeepsRawEventForRepair(t *testing.T) {
	s, _, path := persistedLogFixture(t)
	directory := filepath.Dir(path)
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(context.Background(), persistedRunID, "prompt", persistedText, []byte("Preserved prompt"), 100, false); err == nil {
		t.Fatal("capture hid log write failure")
	}
	events, err := s.AttemptEvents(context.Background(), persistedRunID, 0, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("file failure discarded durable source: %v %v", events, err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(context.Background(), persistedRunID, "output", persistedText, []byte("Recovered output"), 100, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || strings.Count(string(body), "Preserved prompt") != 1 || strings.Count(string(body), "Recovered output") != 1 {
		t.Fatalf("writer did not recover from durable events: %q %v", body, err)
	}
}
