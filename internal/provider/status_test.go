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

func TestProviderConnectivityNeverSendsInference(t *testing.T) {
	exe, _ := os.Executable()
	for _, kind := range []string{"codex", "acp", "claude"} {
		for _, fail := range []bool{false, true} {
			mode := "ok"
			if fail {
				mode = "fail"
			}
			t.Run(kind+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				p := model.Provider{Kind: kind, Command: []string{exe, "-test.run=TestStatusPeer", "--", "status-peer", kind, mode}}
				_, err := CheckConnection(ctx, p, t.TempDir())
				if (err != nil) != fail {
					t.Fatalf("failure=%v error=%v", fail, err)
				}
			})
		}
	}
}

func TestStatusPeer(t *testing.T) {
	args := os.Args
	if len(args) < 4 || args[len(args)-3] != "status-peer" {
		return
	}
	kind, mode := args[len(args)-2], args[len(args)-1]
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var m Message
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(2)
		}
		if m.Method == "initialized" {
			continue
		}
		result, valid := statusPeerResult(kind, m.Method)
		if !valid {
			os.Exit(3)
		}
		frame := map[string]any{"id": m.ID, "result": result}
		if mode == "fail" {
			frame = map[string]any{"id": m.ID, "error": map[string]any{"code": -32000, "message": "Authentication required"}}
		}
		if json.NewEncoder(os.Stdout).Encode(frame) != nil {
			os.Exit(5)
		}
	}
	os.Exit(0)
}

func TestProviderConnectivityCancellationBoundsHungCommands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := CheckConnection(ctx, model.Provider{Kind: "claude", Command: []string{"sh", "-c", "sleep 60"}}, t.TempDir())
	if err == nil || time.Since(started) > 2*time.Second {
		t.Fatalf("hung check not bounded: %v (%v)", err, time.Since(started))
	}
}

func statusPeerResult(kind, method string) (any, bool) {
	if method == "initialize" {
		return map[string]any{"protocolVersion": 1}, true
	}
	switch kind + ":" + method {
	case "codex:account/rateLimits/read":
		return map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 27}}}, true
	case "acp:session/new":
		return map[string]any{"sessionId": "health-session"}, true
	case "claude:models":
		return map[string]any{"models": []any{map[string]any{"id": "test"}}}, true
	default:
		return nil, false
	}
}
