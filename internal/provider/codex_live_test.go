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

// Opt-in real inference test. Enabling it requires working existing Codex login and network.
func TestCodexLiveRoundTrip(t *testing.T) {
	if os.Getenv("WHIRLWIND_TEST_CODEX_LIVE") != "1" {
		t.Skip("set WHIRLWIND_TEST_CODEX_LIVE=1 to verify actual Codex inference")
	}
	logLiveCodexVersion(t)
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

func logLiveCodexVersion(t *testing.T) {
	t.Helper()
	path, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal("live verification requires installed codex:", err)
	}
	versionCtx, versionCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer versionCancel()
	version, err := exec.CommandContext(versionCtx, path, "--version").Output()
	if err != nil {
		t.Fatal("Codex version:", err)
	}
	t.Logf("live executable: %s; version: %s", path, strings.TrimSpace(string(version)))
}

func verifyLiveCodexTurn(t *testing.T, adapter ProcessAdapter, input Input, marker string, n int) Output {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	var mu sync.Mutex
	deltas, completed := 0, false
	out, runErr := adapter.Run(ctx, input, Hooks{Event: func(kind string, _ any) {
		mu.Lock()
		defer mu.Unlock()
		if kind == "item/agentMessage/delta" {
			deltas++
		}
		if kind == "turn/completed" {
			completed = true
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
	if deltas == 0 || !completed {
		t.Errorf("live turn %d missing streamed reply/completion: deltas=%d completed=%t", n+1, deltas, completed)
	}
	mu.Unlock()
	t.Logf("live turn %d: reply matched, streamed deltas=%d, resumed=%t", n+1, deltas, n > 0)
	return out
}
