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

func TestCodexQuotaReadDoesNotStartInference(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	adapter := ProcessAdapter{Provider: model.Provider{Kind: "codex", Command: []string{exe, "-test.run=TestQuotaReaderProcess", "--", "quota-reader"}}}
	reader, ok := any(adapter).(interface {
		ReadQuota(context.Context, string) (string, string, error)
	})
	if !ok {
		t.Fatal("Codex adapter cannot refresh quota without inference")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state, reset, err := reader.ReadQuota(ctx, t.TempDir())
	if err != nil || state != "available" || reset != "" {
		t.Fatalf("metadata-only read: %s %s %v", state, reset, err)
	}
}

func TestQuotaReaderProcess(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "quota-reader" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	step := 0
	for scanner.Scan() {
		var m Message
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch {
		case step == 0 && m.Method == "initialize":
			step++
		case step == 1 && m.Method == "initialized":
			step++
			continue
		case step == 2 && m.Method == "account/rateLimits/read":
			step++
			result = map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 27}}}
		default:
			os.Exit(3)
		}
		if json.NewEncoder(os.Stdout).Encode(map[string]any{"id": m.ID, "result": result}) != nil {
			os.Exit(4)
		}
	}
	os.Exit(0)
}

// Opt-in metadata-only check against the installed authenticated Codex command.
func TestCodexQuotaReadLive(t *testing.T) {
	if os.Getenv("WHIRLWIND_TEST_CODEX_QUOTA_LIVE") != "1" {
		t.Skip("set WHIRLWIND_TEST_CODEX_QUOTA_LIVE=1 to verify native quota discovery")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	adapter := ProcessAdapter{Provider: Defaults("")[0]}
	state, reset, err := adapter.ReadQuota(ctx, t.TempDir())
	if err != nil || (state != "available" && state != "exhausted") {
		t.Fatalf("native quota read: %s %s %v", state, reset, err)
	}
	t.Logf("native quota state=%s reset=%s", state, reset)
}
