package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMemoryLifecycleEventsOmitCredentialQueries(t *testing.T) {
	query := "API_KEY=private-lifecycle-memory-query"
	for _, message := range memoryLifecycleMessages(query) {
		t.Run(message.Method, func(t *testing.T) {
			body, err := json.Marshal(observedParams(message))
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			if strings.Contains(text, query) {
				t.Fatalf("memory credentials retained in %s: %s", message.Method, text)
			}
			if !strings.Contains(text, "query omitted") || !strings.Contains(text, "visible") {
				t.Fatalf("sanitized provenance missing: %s", text)
			}
		})
	}
}

func memoryLifecycleMessages(query string) []Message {
	arguments := map[string]any{"query": query}
	item := map[string]any{"type": "dynamicToolCall", "tool": "search_lessons", "arguments": arguments, "keep": "visible"}
	update := map[string]any{"sessionUpdate": "tool_call", "title": "mcp__whirlwind-memory__search_lessons", "rawInput": arguments, "keep": "visible"}
	block := map[string]any{"type": "tool_use", "name": "mcp__whirlwind-memory__search_lessons", "input": arguments, "keep": "visible"}
	messages := []Message{}
	for _, method := range []string{"item/started", "item/completed"} {
		body, _ := json.Marshal(map[string]any{"item": item})
		messages = append(messages, Message{Method: method, Params: body})
	}
	acp, _ := json.Marshal(map[string]any{"update": update})
	claude, _ := json.Marshal(map[string]any{"message": map[string]any{"content": []any{block}}})
	return append(messages, Message{Method: "session/update", Params: acp}, Message{Method: "agent/event", Params: claude})
}

func TestOtherToolArgumentsRemainAvailableAsEvidence(t *testing.T) {
	raw := json.RawMessage(`{"tool":"other_search","arguments":{"query":"schema"}}`)
	body, err := json.Marshal(observedParams(Message{Method: "item/tool/call", Params: raw}))
	if err != nil || !strings.Contains(string(body), `"query":"schema"`) {
		t.Fatalf("ordinary tool evidence changed: %s %v", body, err)
	}
}
