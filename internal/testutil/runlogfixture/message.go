package runlogfixture

import (
	"context"
	"encoding/json"
	"testing"

	"whirlwind/internal/model"
)

type Capturer interface {
	Capture(context.Context, string, string, string, []byte, int, bool) (model.Artifact, error)
}

func CaptureMessage(t testing.TB, database Capturer, run, id, text string) model.Artifact {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"method": "session/update", "request_id": nil,
		"params": map[string]any{"sessionId": "native", "update": map[string]any{
			"sessionUpdate": "agent_message_chunk", "messageId": id,
			"content": map[string]string{"type": "text", "text": text},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := database.Capture(context.Background(), run, "session/update", "application/json", body, 4096, false)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}
