package store

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

const (
	logRunID    = "run"
	logTextType = "text/plain"
	logJSONType = "application/json"
)

func newRunLogDatabase(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "whirlwind.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Put(context.Background(), "sessions", logRunID, model.Attempt{AttemptID: logRunID}, 0); err != nil {
		t.Fatal(err)
	}
	return database, path
}

func TestRunLogFeedFormatsMessagesAndEventsOnSingleLines(t *testing.T) {
	database, _ := newRunLogDatabase(t)
	ctx := context.Background()
	cases := []struct{ kind, contentType, body, want string }{
		{"prompt", logTextType, "Inspect <auth>.\nKeep APIs.", `message="Inspect <auth>.\nKeep APIs."`},
		{"session/update", logJSONType, `{"method":"session/update","params":{"sessionId":"native","update":{"sessionUpdate":"agent_message_chunk","messageId":"msg-1","content":{"type":"text","text":"First line\nSecond line"}}}}`, `update=agent_message_chunk m_id="msg-1" message="First line\nSecond line"`},
		{"session/update", logJSONType, `{"params":{"update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"Check callers"}}}}`, `data={"params":{"update":{"sessionUpdate":"agent_thought_chunk"}}}`},
		{"whirlwind/stop", logJSONType, "{\n  \"stop_reason\": \"end_turn\"\n}", `data={"stop_reason":"end_turn"}`},
		{"stderr", logTextType, "line\r\n界\t\x1b[31m", `message="line\r\n界\t\u001b[31m"`},
		{"final_output", logTextType, "", `message=""`},
	}
	var want strings.Builder
	for i, test := range cases {
		artifact, err := database.Capture(ctx, logRunID, test.kind, test.contentType, []byte(test.body), 4096, false)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&want, "%s run=%s seq=%d stype=%s %s\n", artifact.CreatedAt, logRunID, i+1, test.kind, test.want)
	}
	feed := runLogFeed{database: database, id: logRunID, seen: map[string]bool{}}
	var output bytes.Buffer
	if err := feed.append(ctx, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != want.String() {
		t.Fatalf("unexpected log lines:\n%s\nwant:\n%s", output.String(), want.String())
	}
	if err := feed.append(ctx, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != want.String() {
		t.Fatal("polling duplicated saved events")
	}
}

func TestRunLogFeedLabelsTruncatedFinalArtifactWithoutEvent(t *testing.T) {
	database, _ := newRunLogDatabase(t)
	ctx := context.Background()
	artifact, err := database.Capture(ctx, logRunID, "final_output", logTextType, []byte("Full\nreply"), 4, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Put(ctx, "sessions", logRunID, model.Attempt{OutputArtifact: artifact.ID}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.Exec("DELETE FROM attempt_events WHERE artifact_id=?", artifact.ID); err != nil {
		t.Fatal(err)
	}
	feed := runLogFeed{database: database, id: logRunID, seen: map[string]bool{}}
	var output bytes.Buffer
	for range 2 {
		if err := feed.append(ctx, &output); err != nil {
			t.Fatal(err)
		}
	}
	want := artifact.CreatedAt + ` run=run stype=final_output truncated=true message="Full"` + "\n"
	if output.String() != want {
		t.Fatalf("unlabeled, multiline, or duplicated final artifact: %q", output.String())
	}
}
