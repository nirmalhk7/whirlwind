package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"whirlwind/internal/execution"
)

// This exercises the actual MCP stdio helper through its advertised schema and Unix broker.
func TestACPCheckMCPAdvertisesAndRunsCommittedID(t *testing.T) {
	ctx := context.Background()
	s := &providerSession{input: Input{ReadOnly: true, Role: "review"}, hooks: Hooks{RunCheck: func(_ context.Context, id string) (CheckResult, error) {
		return CheckResult{CheckID: id, Head: "original-head", Passed: true, Output: "actual-check-result"}, nil
	}}}
	profile, _ := execution.ForPolicy(t.TempDir(), true, "fixture", nil)
	close, err := s.prepareCheckMCP(ctx, &profile)
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	server := s.checkServers[0].(map[string]any)
	args := server["args"].([]string)
	command := append([]string{server["command"].(string)}, args...)
	rpc, err := StartWithProfile(ctx, command, profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.Close()
	var init map[string]any
	if err = rpc.Call(ctx, "initialize", map[string]any{"protocolVersion": "2024-11-05"}, &init); err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err = rpc.Call(ctx, "tools/list", map[string]any{}, &listed); err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != "run_check" {
		t.Fatalf("tool missing: %+v %v", listed, err)
	}
	var result map[string]any
	if err = rpc.Call(ctx, "tools/call", map[string]any{"name": "run_check", "arguments": map[string]string{"check_id": "tests"}}, &result); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(result)
	if !strings.Contains(string(b), "actual-check-result") {
		t.Fatalf("broker output lost: %s", b)
	}
}
