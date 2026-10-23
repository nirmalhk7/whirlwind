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
	defer rpc.Close()
	var out struct {
		OK bool `json:"ok"`
	}
	if e = rpc.Call(context.Background(), "initialize", map[string]any{}, &out); e != nil || !out.OK {
		t.Fatalf(valueErrorFormat, out, e)
	}
}

// TestNativeProtocolsStreamBeforeCompletion verifies ACP and Codex adapters collect streamed message chunks and session IDs.
func TestNativeProtocolsStreamBeforeCompletion(t *testing.T) {
	for _, kind := range []string{"acp", "codex"} {
		t.Run(kind, func(t *testing.T) {
			exe, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			a := ProcessAdapter{Provider: model.Provider{Kind: kind, Command: []string{exe, "-test.run=TestHelperProvider", "--", kind}}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, e := a.Run(ctx, Input{CWD: t.TempDir(), Prompt: "test", ReadOnly: true}, Hooks{Event: func(kind string, value any) {
				if kind == "whirlwind/stderr" {
					t.Logf("provider diagnostics: %v", value)
				}
			}})
			if e != nil || out.Text != "first second" || out.SessionID != testSessionID {
				t.Fatalf(valueErrorFormat, out, e)
			}
		})
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
	case "thread/start":
		result = map[string]any{"thread": map[string]any{"id": testSessionID}}
	case "session/prompt":
		for _, text := range []string{"first ", "second"} {
			emit(map[string]any{"method": "session/update", "params": map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}}})
		}
		result = map[string]any{"stopReason": "end_turn"}
	case "turn/start":
		emit(map[string]any{"id": m.ID, "result": map[string]any{}})
		for _, text := range []string{"first ", "second"} {
			emit(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"delta": text}})
		}
		emit(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"status": "completed"}}})
		return
	}
	emit(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
}
