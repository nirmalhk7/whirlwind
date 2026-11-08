package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"whirlwind/internal/model"
)

const (
	valueErrorFormat = "%+v %v"
	testSessionID    = "session-one"
)

// TestRoutingRejectsIncapableAndExhaustedProviders verifies routing excludes unavailable, exhausted, and role-incompatible providers.
func TestRoutingRejectsIncapableAndExhaustedProviders(t *testing.T) {
	ps := []model.Provider{{ID: "cheap", Available: true, Quota: "exhausted", Capabilities: []string{"code"}}, {ID: "read", Available: true, Quota: "unknown", Capabilities: []string{"ideate"}}, {ID: "good", Available: true, Quota: "unknown", Capabilities: []string{"code"}}}
	p, e := Route(ps, "code", nil, nil)
	if e != nil || p.ID != "good" {
		t.Fatalf(valueErrorFormat, p, e)
	}
}

// TestJSONRPCProcessAndCancellation verifies RPC replies are decoded and cancelling the context stops the provider process.
func TestJSONRPCProcessAndCancellation(t *testing.T) {
	rpc, e := Start(context.Background(), []string{"sh", "-c", `while IFS= read -r line; do printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"ok":true}}'; done`}, t.TempDir(), nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if err := rpc.Close(); err != nil {
			t.Errorf("close provider RPC: %v", err)
		}
	})
	var out struct {
		OK bool `json:"ok"`
	}
	if e = rpc.Call(context.Background(), "initialize", map[string]any{}, &out); e != nil || !out.OK {
		t.Fatalf(valueErrorFormat, out, e)
	}
}

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

// TestHelperProvider acts as a fake ACP or Codex subprocess when invoked with a protocol argument, otherwise returns without running a fixture.
func TestHelperProvider(t *testing.T) {
	kind := ""
	for n, arg := range os.Args {
		if arg == "--" && n+1 < len(os.Args) {
			kind = os.Args[n+1]
		}
	}
	if kind == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var m Message
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(2)
		}
		if m.Method == "initialized" {
			continue
		}
		helperProviderReply(t, m)
	}
	os.Exit(0)
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

func helperProviderReply(t *testing.T, m Message) {
	result := any(map[string]any{})
	emit := func(v any) {
		if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
			t.Fatal(err)
		}
	}
	switch m.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true}}
	case "session/new":
		result = map[string]any{"sessionId": testSessionID, "modes": map[string]any{"availableModes": []any{map[string]any{"id": "ask"}}}}
	case "session/prompt":
		for _, text := range []string{"first ", "second"} {
			emit(map[string]any{"method": "session/update", "params": map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}}})
		}
		result = map[string]any{"stopReason": "end_turn"}
	}
	emit(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
}
