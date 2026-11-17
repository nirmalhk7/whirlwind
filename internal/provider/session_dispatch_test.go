package provider

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"whirlwind/internal/model"
)

// TestACPProtocolStreamsBeforeCompletion verifies the supported ACP adapter collects streamed chunks and session IDs.
func TestACPProtocolStreamsBeforeCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: "stream", ReadOnly: true, Chunks: []string{"first ", "second"}})
	out, err := a.Run(ctx, Input{CWD: t.TempDir(), Prompt: contractPrompt, ReadOnly: true}, Hooks{Event: func(kind string, value any) {
		if kind == "whirlwind/stderr" {
			t.Logf("provider diagnostics: %v", value)
		}
	}})
	if err != nil || out.Text != "first second" || out.SessionID != testSessionID {
		t.Fatalf(valueErrorFormat, out, err)
	}
}

// TestNativeQuestionResponseUsesProtocolShape verifies Codex user answers use the nested answers-array response contract.
func TestNativeQuestionResponseUsesProtocolShape(t *testing.T) {
	hooks := Hooks{Questions: func(context.Context, []model.Question) (map[string]string, error) {
		return map[string]string{"callback": "localhost"}, nil
	}}
	result, err := AnswerQuestions(context.Background(), hooks, []model.Question{{ID: "callback", Text: "Which callback?"}}, "codex")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(result)
	if string(b) != `{"answers":{"callback":{"answers":["localhost"]}}}` {
		t.Fatal(string(b))
	}
}
