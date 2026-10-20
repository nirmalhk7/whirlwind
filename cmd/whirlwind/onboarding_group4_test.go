package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"whirlwind/internal/provider"
)

func TestOnboardingAgentHelper(t *testing.T) {
	kind := onboardingAgentKind()
	if kind == "" {
		return
	}
	emit := func(value any) {
		if json.NewEncoder(os.Stdout).Encode(value) != nil {
			os.Exit(2)
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var m provider.Message
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(2)
		}
		result := any(map[string]any{})
		switch m.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true}}
		case "initialized":
			continue
		case "session/new":
			result = map[string]any{"sessionId": onboardingSessionID}
		case "thread/start":
			result = map[string]any{"thread": map[string]any{"id": onboardingSessionID}}
		case "session/prompt", "turn/start":
			if !validOnboardingPrompt(kind, m, emit) {
				continue
			}
			streamOnboardingReply(kind, m, emit)
			continue
		}
		emit(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
	}
	if scanner.Err() != nil {
		fmt.Fprintln(os.Stderr, scanner.Err())
		os.Exit(2)
	}
	os.Exit(0)
}

func validOnboardingPrompt(kind string, m provider.Message, emit func(any)) bool {
	key := "prompt"
	if kind == "codex" {
		key = "input"
	}
	var params map[string]json.RawMessage
	var prompt []struct{ Text string }
	_ = json.Unmarshal(m.Params, &params)
	_ = json.Unmarshal(params[key], &prompt)
	if len(prompt) != 1 || (!strings.Contains(prompt[0].Text, "Generate exactly four") && !strings.Contains(prompt[0].Text, "Candidate ideas:")) || !strings.Contains(prompt[0].Text, "Versioned handoff:") {
		emit(map[string]any{"id": m.ID, "error": map[string]any{"code": -32602, "message": "workflow prompt missing"}})
		return false
	}
	return true
}

func streamOnboardingReply(kind string, m provider.Message, emit func(any)) {
	if kind == "codex" {
		emit(map[string]any{"id": m.ID, "result": map[string]any{"turn": map[string]any{"id": onboardingTurnID, "status": "inProgress"}}})
	}
	reply := onboardingReply
	var params map[string]json.RawMessage
	_ = json.Unmarshal(m.Params, &params)
	var content []struct{ Text string }
	key := "prompt"
	if kind == "codex" {
		key = "input"
	}
	_ = json.Unmarshal(params[key], &content)
	if len(content) == 1 && strings.Contains(content[0].Text, "Candidate ideas:") {
		reply = `{"unique":true}`
	}
	middle := len(reply) / 2
	for _, text := range []string{reply[:middle], reply[middle:]} {
		if kind == "acp" {
			emit(map[string]any{"method": "session/update", "params": map[string]any{"sessionId": onboardingSessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}}}})
		} else {
			emit(map[string]any{"method": "item/agentMessage/delta", "params": map[string]string{"threadId": onboardingSessionID, "turnId": onboardingTurnID, "itemId": "message", "delta": text}})
		}
	}
	if kind == "codex" {
		emit(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": onboardingSessionID, "turn": map[string]any{"id": onboardingTurnID, "status": "completed", "items": []any{}}}})
		return
	}
	emit(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]string{"stopReason": "end_turn"}})
}

func onboardingAgentKind() string {
	kind := ""
	for n, arg := range os.Args {
		if arg == "--" && n+1 < len(os.Args) {
			kind = os.Args[n+1]
		}
	}
	return kind
}
