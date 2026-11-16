package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

func TestRunLogPathBackfillsHistoricalRunOnce(t *testing.T) {
	s, databasePath, path := persistedLogFixture(t)
	if _, err := s.Capture(context.Background(), persistedRunID, "prompt", persistedText, []byte("Historical prompt"), 100, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReadOnly(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	actual, err := reader.RunLogPath(context.Background(), persistedRunID)
	if err != nil || actual != path {
		t.Fatalf("historical log unavailable: %s %v", actual, err)
	}
	before, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(before), `message="Historical prompt"`) {
		t.Fatalf("historical events were not backfilled: %q %v", before, err)
	}
	if _, err := reader.RunLogPath(context.Background(), persistedRunID); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("opening log duplicated historical lines: %q %v", after, err)
	}
}

func TestRunLogWriterRepairsPartialLastLineAfterRestart(t *testing.T) {
	s, databasePath, path := persistedLogFixture(t)
	if _, err := s.Capture(context.Background(), persistedRunID, "output", persistedText, []byte("first"), 100, false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, int64(len(before)-4)); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close() }()
	if _, err := restarted.Capture(context.Background(), persistedRunID, "output", persistedText, []byte("next"), 100, false); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(after), string(before)) || strings.Count(string(after), "\n") != 2 {
		t.Fatalf("partial line was lost or duplicated: %q %v", after, err)
	}
}

func TestRunLogWriterUsesRedactedSavedContent(t *testing.T) {
	s, _, path := persistedLogFixture(t)
	if _, err := s.Capture(context.Background(), persistedRunID, "prompt", persistedText, []byte("api_key=dont-print-this"), 100, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(body), "dont-print-this") || !strings.Contains(string(body), "[redacted]") {
		t.Fatalf("run log did not preserve storage redaction: %q %v", body, err)
	}
}

func TestRunLogWriterFlushesAfterBatchCompletion(t *testing.T) {
	s, _, path := persistedLogFixture(t)
	captureStoredChunk(t, s, "Batch finished")
	if err := s.Write(context.Background(), []Change{{Kind: "sessions", ID: persistedRunID, Value: model.Attempt{Finished: true}, Expected: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), `message="Batch finished"`) {
		t.Fatalf("batch completion omitted pending message: %q %v", body, err)
	}
}

func TestRunLogWriterFlushesAfterUsageCompletion(t *testing.T) {
	s, _, path := persistedLogFixture(t)
	captureStoredChunk(t, s, "Usage finished")
	entry := model.UsageEntry{AttemptID: persistedRunID, TaskID: "task"}
	changes := []Change{{Kind: "sessions", ID: persistedRunID, Value: model.Attempt{Finished: true}, Expected: 1}}
	if err := s.CompleteUsage(context.Background(), entry, changes); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), `message="Usage finished"`) {
		t.Fatalf("normal completion omitted pending message: %q %v", body, err)
	}
}

func TestRunLogWriterFlushesAfterInterruptedRecovery(t *testing.T) {
	s, _, path := persistedLogFixture(t)
	captureStoredChunk(t, s, "Interrupted reply")
	if err := s.RecoverInterrupted(context.Background(), "replacement-owner"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), `message="Interrupted reply"`) {
		t.Fatalf("recovery omitted saved message fragments: %q %v", body, err)
	}
}
