package provider

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

const (
	valueErrorFormat = "%+v %v"
	testSessionID    = "session-one"
)

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
