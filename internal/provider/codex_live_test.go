package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in ACP real inference test. Enabling it requires a configured Codex ACP adapter, login and network.
func TestCodexACPLiveRoundTrip(t *testing.T) {
	if os.Getenv("WHIRLWIND_TEST_CODEX_LIVE") != "1" {
		t.Skip("set WHIRLWIND_TEST_CODEX_LIVE=1 to verify real Codex ACP inference")
	}
	configured := Defaults("")[0]
	if !Available(configured) {
		t.Skip("configured Codex ACP adapter is not installed")
	}
	logLiveACPCommand(t, configured.Command)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	marker := "WHIRLWIND_" + hex.EncodeToString(nonce[:])
	adapter := ProcessAdapter{Provider: Defaults("")[0]}
	input := Input{Role: "plan", CWD: t.TempDir(), ReadOnly: true}
	prompts := []string{
		fmt.Sprintf("Do not use tools or inspect files. Remember this marker for our next turn: %s. Reply with only that marker.", marker),
		"Do not use tools or inspect files. Reply with only the exact marker I gave you in our previous turn.",
	}
	for n, prompt := range prompts {
		input.Prompt = prompt
		out := verifyLiveCodexTurn(t, adapter, input, marker, n)
		input.SessionID = out.SessionID
	}
}

func logLiveACPCommand(t *testing.T, command []string) {
	t.Helper()
	path, err := exec.LookPath(command[0])
	if err != nil {
		t.Skip("live verification requires installed Codex ACP adapter:", err)
	}
	t.Logf("live ACP adapter executable: %s", path)
}

func verifyLiveCodexTurn(t *testing.T, adapter ProcessAdapter, input Input, marker string, n int) Output {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	var mu sync.Mutex
	deltas, stopped := 0, false
	out, runErr := adapter.Run(ctx, input, Hooks{Event: func(kind string, _ any) {
		mu.Lock()
		defer mu.Unlock()
		if kind == "item/agentMessage/delta" {
			deltas++
		}
		if kind == "whirlwind/stop" {
			stopped = true
		}
	}})
	cancel()
	if runErr != nil {
		t.Fatalf("live turn %d failed (login, quota, network, or protocol): %v", n+1, runErr)
	}
	if out.SessionID == "" || (n > 0 && out.SessionID != input.SessionID) {
		t.Fatalf("live turn %d session ID lost: %+v", n+1, out)
	}
	if strings.TrimSpace(out.Text) != marker {
		t.Fatalf("live turn %d reply mismatch: %q", n+1, out.Text)
	}
	mu.Lock()
	if deltas == 0 || !stopped {
		t.Errorf("live turn %d missing streamed reply/completion: deltas=%d stopped=%t", n+1, deltas, stopped)
	}
	mu.Unlock()
	t.Logf("live turn %d: reply matched, streamed deltas=%d, resumed=%t", n+1, deltas, n > 0)
	return out
}
