package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Accepting extra command arguments would let a registered tool bypass committed check IDs.
func TestReviewerToolRejectsArbitraryArguments(t *testing.T) {
	s := &providerSession{input: Input{ReadOnly: true, Role: "review"}, hooks: Hooks{RunCheck: func(context.Context, string) (CheckResult, error) {
		t.Fatal("unexpected broker execution")
		return CheckResult{}, nil
	}}}
	_, err := s.handle(context.Background(), Message{Method: "whirlwind/run_check", Params: json.RawMessage(`{"check_id":"tests","command":["sh","-c","bad"]}`)})
	if err == nil {
		t.Fatal("arbitrary command accepted")
	}
}

// The native Codex dynamic tool contract must expose check results as callable tool output.
func TestNativeReviewerToolUsesRegisteredCheck(t *testing.T) {
	s := &providerSession{input: Input{ReadOnly: true, Role: "review"}, hooks: Hooks{RunCheck: func(_ context.Context, id string) (CheckResult, error) {
		if id != "tests" {
			t.Fatalf("unexpected ID %s", id)
		}
		return CheckResult{CheckID: id, Head: "commit", Passed: true, Output: "verified"}, nil
	}}}
	got, err := s.handle(context.Background(), Message{Method: "item/tool/call", Params: json.RawMessage(`{"tool":"run_check","arguments":{"check_id":"tests"}}`)})
	b, _ := json.Marshal(got)
	if err != nil || !strings.Contains(string(b), "verified") || !strings.Contains(string(b), "contentItems") {
		t.Fatalf("tool response %s %v", b, err)
	}
	tools := s.checkTools()
	if len(tools) != 1 {
		t.Fatal("reviewer tool not advertised")
	}
	s.input.ReadOnly = false
	if len(s.checkTools()) != 0 {
		t.Fatal("check tool advertised to mutable role")
	}
}
