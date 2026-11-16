package dashboard

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/runlogfixture"
)

const logUpdateType = "session/update"

func captureLogMessage(t *testing.T, database *store.Store, id, text string) model.Artifact {
	t.Helper()
	return runlogfixture.CaptureMessage(t, database, logRunID, id, text)
}

func TestRunTailShowsFullMessageAsOneLine(t *testing.T) {
	database, path := newRunLogDatabase(t)
	if _, err := database.Capture(context.Background(), logRunID, "prompt", logTextType, []byte("Ready"), 100, false); err != nil {
		t.Fatal(err)
	}
	captureLogMessage(t, database, "live", "Combined")
	capture := captureRunTail(t, "AGGREGATED DONE")
	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			body, _ := os.ReadFile(capture)
			if bytes.Contains(body, []byte("stype=prompt")) {
				body := []byte(`{"params":{"update":{"sessionUpdate":"agent_message_chunk","messageId":"live","content":{"type":"text","text":" AGGREGATED DONE"}}}}`)
				_, err := database.Capture(context.Background(), logRunID, logUpdateType, logJSONType, body, 4096, false)
				if err == nil {
					_, err = database.Capture(context.Background(), logRunID, "whirlwind/stop", logJSONType, []byte(`{}`), 100, false)
				}
				done <- err
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		done <- os.ErrDeadlineExceeded
	}()
	if err := followRunLogs(path, logRunID); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(body), "\n") != 2 || !bytes.Contains(body, []byte(`m_id="live" message="Combined AGGREGATED DONE"`)) {
		t.Fatalf("tail displayed token fragments instead of full reply: %q", body)
	}
}
