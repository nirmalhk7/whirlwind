package provider

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

const (
	memoryFixtureCommand     = "/memory"
	memoryStdioContractError = "memory stdio contract incorrect"
)

func TestNativeMemoryToolCallsHarness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := Input{CWD: t.TempDir(), Prompt: contractPrompt, Memory: &MemoryConnection{Command: memoryFixtureCommand, URL: "http://127.0.0.1:1", Token: "attempt-only"}}
	calls := 0
	out, err := contractAdapter(t, protocolFixture{Kind: "codex", Scenario: "memory"}).Run(ctx, input, Hooks{SearchLessons: func(_ context.Context, query string) (string, error) {
		calls++
		if query != "contention" {
			t.Fatal(query)
		}
		return "use bounded retries", nil
	}})
	if err != nil || out.Text != contractReply || calls != 1 {
		t.Fatalf("memory contract: %+v %v calls=%d", out, err, calls)
	}
}

func TestACPRegistersScopedMemoryServerOnResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := Input{CWD: t.TempDir(), Prompt: contractPrompt, SessionID: testSessionID, Memory: &MemoryConnection{Command: memoryFixtureCommand, URL: "http://127.0.0.1:1", Token: "attempt-only"}}
	out, err := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: "memory", Resume: true}).Run(ctx, input, Hooks{})
	if err != nil || out.Text != contractReply {
		t.Fatalf("memory resume: %+v %v", out, err)
	}
}

func (p *protocolPeer) memoryCallback() {
	p.callback("item/tool/call", map[string]any{"threadId": testSessionID, "turnId": contractTurnID, "callId": "memory-one", "tool": "search_lessons", "arguments": map[string]any{"query": "contention"}}, map[string]any{"success": true, "contentItems": []any{map[string]any{"type": "inputText", "text": "use bounded retries"}}})
}

func (p *protocolPeer) requireMemoryServer(value any) {
	b, _ := json.Marshal(value)
	var servers []struct {
		Name    string                         `json:"name"`
		Command string                         `json:"command"`
		Args    []string                       `json:"args"`
		Env     []struct{ Name, Value string } `json:"env"`
	}
	p.require(json.Unmarshal(b, &servers) == nil && len(servers) == 1, "memory server missing")
	server := servers[0]
	p.require(server.Name == "whirlwind-memory" && server.Command == memoryFixtureCommand, memoryStdioContractError)
	p.require(len(server.Args) == 1 && server.Args[0] == "memory-mcp", memoryStdioContractError)
	p.require(len(server.Env) == 2, memoryStdioContractError)
}
