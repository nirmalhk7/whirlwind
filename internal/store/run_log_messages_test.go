package store

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/testutil/runlogfixture"
)

func captureLogMessage(t *testing.T, database *Store, id, text string) model.Artifact {
	t.Helper()
	return runlogfixture.CaptureMessage(t, database, logRunID, id, text)
}

func TestRunLogMessagesJoinAcrossPollsAndIncidentalEvents(t *testing.T) {
	const messageID = "msg-1"
	database, _ := newRunLogDatabase(t)
	first := captureLogMessage(t, database, messageID, "Share")
	feed := runLogFeed{database: database, id: logRunID, seen: map[string]bool{}}
	var output bytes.Buffer
	appendLogPoll(t, &feed, &output)
	if output.Len() != 0 {
		t.Fatalf("unfinished message leaked a fragment: %q", output.String())
	}
	if _, err := database.Capture(context.Background(), logRunID, "usage", logJSONType, []byte(`{"used":10}`), 100, false); err != nil {
		t.Fatal(err)
	}
	captureLogMessage(t, database, messageID, " the identical area-search")
	appendLogPoll(t, &feed, &output)
	if output.Len() != 0 {
		t.Fatal("poll or usage event split the message")
	}
	captureLogMessage(t, database, messageID, " test cleanup lifecycle")
	if _, err := database.Capture(context.Background(), logRunID, "whirlwind/stop", logJSONType, []byte(`{"stop_reason":"end_turn"}`), 100, false); err != nil {
		t.Fatal(err)
	}
	appendLogPoll(t, &feed, &output)
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	want := first.CreatedAt + ` run=run seq=1 stype=session/update update=agent_message_chunk m_id="msg-1" message="Share the identical area-search test cleanup lifecycle"`
	if len(lines) != 3 || lines[0] != want || !strings.Contains(lines[1], "seq=2 stype=usage") || !strings.Contains(lines[2], "stype=whirlwind/stop") {
		t.Fatalf("message text, first-chunk identity, or event order lost: %q", output.String())
	}
	before := output.String()
	appendLogPoll(t, &feed, &output)
	if output.String() != before {
		t.Fatal("completed message duplicated on next poll")
	}
}

func TestRunLogMessagesSeparateIDsAndFlushFinishedAttempt(t *testing.T) {
	database, _ := newRunLogDatabase(t)
	first := captureLogMessage(t, database, "first", "First reply")
	second := captureLogMessage(t, database, "second", "Second\nreply")
	feed := runLogFeed{database: database, id: logRunID, seen: map[string]bool{}}
	var output bytes.Buffer
	appendLogPoll(t, &feed, &output)
	want := first.CreatedAt + ` run=run seq=1 stype=session/update update=agent_message_chunk m_id="first" message="First reply"` + "\n"
	if output.String() != want {
		t.Fatalf("new message ID did not complete previous message: %q", output.String())
	}
	if err := database.Put(context.Background(), "sessions", logRunID, model.Attempt{Finished: true}, 1); err != nil {
		t.Fatal(err)
	}
	appendLogPoll(t, &feed, &output)
	want += second.CreatedAt + ` run=run seq=2 stype=session/update update=agent_message_chunk m_id="second" message="Second\nreply"` + "\n"
	if output.String() != want {
		t.Fatalf("finished attempt omitted or combined distinct replies: %q", output.String())
	}
}

func TestRunLogMessagesJoinAcrossEventPages(t *testing.T) {
	database, _ := newRunLogDatabase(t)
	for range 105 {
		captureLogMessage(t, database, "paged", "x")
	}
	if err := database.Put(context.Background(), "sessions", logRunID, model.Attempt{Finished: true}, 1); err != nil {
		t.Fatal(err)
	}
	feed := runLogFeed{database: database, id: logRunID, seen: map[string]bool{}}
	var output bytes.Buffer
	appendLogPoll(t, &feed, &output)
	if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), `m_id="paged" message="`+strings.Repeat("x", 105)+`"`) {
		t.Fatalf("pagination split or lost message fragments: %q", output.String())
	}
}

func TestRunLogMessagesDrainFinalChunksBeforeCompletion(t *testing.T) {
	database, _ := newRunLogDatabase(t)
	first := captureLogMessage(t, database, "ending", "First")
	feed := runLogFeed{database: database, id: logRunID, seen: map[string]bool{}}
	var output bytes.Buffer
	appendLogPoll(t, &feed, &output)
	// A final chunk can be saved after the event read, before finished status is read.
	captureLogMessage(t, database, "ending", " last")
	if err := feed.finish(context.Background(), &output, model.Attempt{Finished: true}); err != nil {
		t.Fatal(err)
	}
	want := first.CreatedAt + ` run=run seq=1 stype=session/update update=agent_message_chunk m_id="ending" message="First last"` + "\n"
	if output.String() != want {
		t.Fatalf("completion flushed before final durable chunks: %q", output.String())
	}
}

func appendLogPoll(t *testing.T, feed *runLogFeed, output *bytes.Buffer) {
	t.Helper()
	if err := feed.append(context.Background(), output); err != nil {
		t.Fatal(err)
	}
}
