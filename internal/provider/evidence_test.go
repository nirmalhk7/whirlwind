package provider

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCumulativeUsageIsLabeledSessionScope(t *testing.T) {
	s := &providerSession{}
	s.tokenUsage(map[string]any{"tokenUsage": map[string]any{"total": map[string]any{"totalTokens": float64(120), "inputTokens": float64(100), "outputTokens": float64(20)}, "last": map[string]any{"totalTokens": float64(12)}}})
	if s.output.Usage == nil || s.output.Usage.Scope != "session" || s.output.Usage.TotalTokens == nil || *s.output.Usage.TotalTokens != 120 {
		t.Fatalf("cumulative usage mislabeled: %+v", s.output)
	}
}
func TestACPReplayHasSeparateEvidence(t *testing.T) {
	kinds := []string{}
	s := &providerSession{hooks: Hooks{Event: func(kind string, _ any) { kinds = append(kinds, kind) }}}
	s.replaying.Store(true)
	_, _ = s.handle(context.Background(), Message{Method: "session/update", Params: json.RawMessage(`{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"old reply"}}}`)})
	if len(kinds) != 1 || kinds[0] != "whirlwind/replay" {
		t.Fatal(kinds)
	}
}

func TestACPStopReasonIsObservable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopped := false
	_, err := contractAdapter(t, protocolFixture{Kind: "acp"}).Run(ctx, Input{CWD: t.TempDir(), Prompt: contractPrompt}, Hooks{Event: func(kind string, _ any) {
		if kind == "whirlwind/stop" {
			stopped = true
		}
	}})
	if err != nil || !stopped {
		t.Fatal("ACP stop reason missing", err)
	}
}
func TestClaudeStreamRetainsNativeSessionBeforeFailure(t *testing.T) {
	s := &providerSession{}
	_, _ = s.handle(context.Background(), Message{Method: "agent/event", Params: json.RawMessage(`{"type":"system","session_id":"claude-native","resolved_model":"actual"}`)})
	if s.output.SessionID != "claude-native" || s.output.ResolvedModel != "actual" {
		t.Fatal("Claude failure loses session identity", s.output)
	}
}

func TestACPTerminalEmitsUnpolledOutputAndExit(t *testing.T) {
	var mu sync.Mutex
	events := map[string][]map[string]any{}
	c := NewClient(Input{CWD: t.TempDir()}, Hooks{
		Permission: func(context.Context, Permission) (bool, error) { return true, nil },
		Event: func(kind string, value any) {
			mu.Lock()
			defer mu.Unlock()
			events[kind] = append(events[kind], value.(map[string]any))
		},
	})
	defer c.Close()
	ctx := context.Background()
	v, err := c.Handle(ctx, clientMessage("terminal/create", map[string]any{"command": "sh", "args": []string{"-c", "printf 'unpolled output'; exit 7"}}))
	if err != nil {
		t.Fatal(err)
	}
	id := v.(map[string]any)["terminalId"].(string)
	// Wait locally, without issuing terminal/output or wait_for_exit requests.
	c.mu.Lock()
	term := c.terminals[id]
	c.mu.Unlock()
	select {
	case <-term.done:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal did not exit")
	}
	mu.Lock()
	defer mu.Unlock()
	output := ""
	for _, e := range events["whirlwind/terminal/outputDelta"] {
		if e["terminalId"] != id {
			t.Fatal("terminal linkage lost", e)
		}
		output += e["delta"].(string)
	}
	exits := events["whirlwind/terminal/exit"]
	if !strings.Contains(output, "unpolled output") || len(exits) != 1 || exits[0]["exitCode"] != 7 || exits[0]["terminalId"] != id {
		t.Fatal("unpolled terminal evidence lost", events)
	}
}
