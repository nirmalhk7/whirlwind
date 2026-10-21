package provider

import (
	"context"
	"testing"
	"time"
)

func TestUnsupportedNativeMemoryKeepsAutomaticWorkflowAvailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	warned := false
	input := Input{CWD: t.TempDir(), Prompt: contractPrompt, Memory: &MemoryConnection{Command: "/memory", URL: "http://127.0.0.1:1", Token: "attempt-only"}}
	out, err := contractAdapter(t, protocolFixture{Kind: "codex", Scenario: "memory-unsupported"}).Run(ctx, input, Hooks{Event: func(kind string, _ any) {
		if kind == "whirlwind/capability_warning" {
			warned = true
		}
	}})
	if err != nil || out.Text != contractReply || !warned {
		t.Fatalf("capability fallback: %+v %v warning=%v", out, err, warned)
	}
}

func (p *protocolPeer) rejectUnsupportedMemory(f protocolFixture) {
	if f.Scenario != "memory-unsupported" {
		return
	}
	p.emit(map[string]any{"jsonrpc": "2.0", "id": p.message.ID, "error": map[string]any{"code": -32602, "message": "dynamicTools unsupported"}})
	params := p.read("thread/start")
	p.require(params["dynamicTools"] == nil, "unsupported dynamic tools retried")
}
