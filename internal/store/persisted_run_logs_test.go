package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

const (
	persistedRunID = "stored-run"
	persistedText  = "text/plain"
)

func persistedLogFixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "whirlwind.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Put(context.Background(), "sessions", persistedRunID, model.Attempt{AttemptID: persistedRunID}, 0); err != nil {
		t.Fatal(err)
	}
	return s, path, filepath.Join(filepath.Dir(path), "run-logs", persistedRunID+".log")
}

func TestCaptureWritesFormattedRunLogBeforeViewing(t *testing.T) {
	s, _, path := persistedLogFixture(t)
	a, err := s.Capture(context.Background(), persistedRunID, "prompt", persistedText, []byte("Inspect <auth>.\nKeep APIs."), 1000, false)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("capture did not create persistent run log: %v", err)
	}
	want := a.CreatedAt + ` run=stored-run seq=1 stype=prompt message="Inspect <auth>.\nKeep APIs."` + "\n"
	if string(body) != want {
		t.Fatalf("log was not written in display format: %q", body)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("run log permissions are not private: %v", err)
	}
}

func captureStoredChunk(t *testing.T, s *Store, text string) model.Artifact {
	t.Helper()
	body, err := json.Marshal(map[string]any{"params": map[string]any{"update": map[string]any{
		"sessionUpdate": "agent_message_chunk", "messageId": "message",
		"content": map[string]string{"type": "text", "text": text},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Capture(context.Background(), persistedRunID, "session/update", "application/json", body, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestRunLogWriterCombinesChunksAcrossStoreRestart(t *testing.T) {
	s, databasePath, path := persistedLogFixture(t)
	first := captureStoredChunk(t, s, "Full")
	other, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	captureStoredChunk(t, other, " message")
	if _, err := other.Capture(context.Background(), persistedRunID, "whirlwind/stop", "application/json", []byte(`{"stop_reason":"end_turn"}`), 100, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := first.CreatedAt + ` run=stored-run seq=1 stype=session/update update=agent_message_chunk m_id="message" message="Full message"` + "\n"
	if !strings.HasPrefix(string(body), want) || strings.Count(string(body), "\n") != 2 {
		t.Fatalf("restart lost chunks or duplicated messages: %q", body)
	}
}

func TestRunLogWriterResumesExistingFileWithoutDuplicates(t *testing.T) {
	s, databasePath, path := persistedLogFixture(t)
	if _, err := s.Capture(context.Background(), persistedRunID, "output", persistedText, []byte("first"), 100, false); err != nil {
		t.Fatal(err)
	}
	other, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if _, err := other.Capture(context.Background(), persistedRunID, "output", persistedText, []byte("second"), 100, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(context.Background(), persistedRunID, "output", persistedText, []byte("third"), 100, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first", "second", "third"} {
		if strings.Count(string(body), `message="`+text+`"`) != 1 {
			t.Fatalf("existing log was rewritten or duplicated: %q", body)
		}
	}
}

func TestFinishedRunFlushesPersistedMessage(t *testing.T) {
	s, _, path := persistedLogFixture(t)
	captureStoredChunk(t, s, "Finished reply")
	if err := s.Put(context.Background(), "sessions", persistedRunID, model.Attempt{Finished: true}, 1); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), `message="Finished reply"`) {
		t.Fatalf("finished run did not persist buffered reply: %q %v", body, err)
	}
}
