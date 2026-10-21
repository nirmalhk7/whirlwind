package provider

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
	"whirlwind/internal/model"
)

func (p *protocolPeer) configureACPFixtureThinking(f protocolFixture) {
	p.reply(map[string]any{"configOptions": []any{
		map[string]any{"id": contractModelOptionID, "category": "model", "type": "select", "currentValue": contractSelectedModel, "options": []any{map[string]any{"value": contractSelectedModel, "name": contractSelectedModelName}}},
		map[string]any{"id": "effort", "name": "Thinking", "category": "thought_level", "type": "select", "currentValue": "low", "options": []any{map[string]any{"group": "levels", "name": "Levels", "options": []any{map[string]any{"value": "high", "name": "High"}}}}},
	}})
	if f.Scenario == contractThinkingCatalog {
		for p.scanner.Scan() {
		}
		return
	}
	params := p.read("session/set_config_option")
	p.require(params["configId"] == "effort" && params["value"] == "high" && params["sessionId"] == testSessionID, "thinking setting missing")
	p.reply(map[string]any{"configOptions": []any{
		map[string]any{"id": contractModelOptionID, "category": "model", "type": "select", "currentValue": contractSelectedModel, "options": []any{map[string]any{"value": contractSelectedModel, "name": contractSelectedModelName}}},
		map[string]any{"id": "effort", "name": "Thinking", "category": "thought_level", "type": "select", "currentValue": "high", "options": []any{map[string]any{"value": "high", "name": "High"}}},
	}})
}

func (p *protocolPeer) codexFixtureSessionReply(f protocolFixture, params map[string]any, id string) {
	p.rejectUnsupportedMemory(f)
	if f.Scenario == "memory" {
		tools, ok := params["dynamicTools"].([]any)
		p.require(ok && len(tools) == 1, "dynamic memory tool missing")
	}
	sandbox := "workspace-write"
	if f.ReadOnly {
		sandbox = "read-only"
	}
	p.require(params["sandbox"] == sandbox && params["approvalPolicy"] == "untrusted" && params["approvalsReviewer"] == "user", "thread policy lost")
	if f.Model != "" {
		p.require(params["model"] == f.Model, "Codex selected model missing")
	}
	p.reply(map[string]any{"thread": map[string]any{"id": id, "turns": []any{}}})
}

func (p *protocolPeer) readFixturePrompt(f protocolFixture) {
	method := "session/prompt"
	idKey, promptKey := "sessionId", "prompt"
	if f.Kind == "codex" {
		method, idKey, promptKey = "turn/start", "threadId", "input"
	}
	params := p.read(method)
	p.require(params[idKey] == testSessionID, "prompt session ID missing")
	p.require(reflect.DeepEqual(params[promptKey], []any{map[string]any{"type": "text", "text": contractPrompt}}), "outgoing prompt text lost")
}

func (p *protocolPeer) stopFixturePrompt(f protocolFixture) bool {
	if f.Scenario == "malformed" {
		fmt.Println("not JSON")
		for p.scanner.Scan() {
		}
		return true
	}
	if f.Scenario == "exit" {
		return true
	}
	if f.Kind == "codex" {
		p.reply(map[string]any{"turn": map[string]any{"id": contractTurnID, "items": []any{}, "status": "inProgress"}})
	}
	if f.Scenario == "stall" {
		p.notify("fixture/prompt", map[string]any{})
		for p.scanner.Scan() {
		}
		return true
	}
	return false
}

func (p *protocolPeer) fixtureCallbacks(f protocolFixture, cwd string) {
	if f.Scenario == "memory" && f.Kind == "codex" {
		p.memoryCallback()
	}
	if f.Scenario == "allow" || f.Scenario == "deny" {
		if f.Kind == "codex" {
			p.callback("item/tool/requestUserInput", map[string]any{"threadId": testSessionID, "turnId": contractTurnID, "itemId": "question-one", "isBlocking": true, "questions": []any{map[string]any{"id": "db", "header": "Database", "question": "Which database?", "options": []any{map[string]any{"label": "SQLite", "description": "Local"}, map[string]any{"label": "PostgreSQL", "description": "Remote"}}}}}, map[string]any{"answers": map[string]any{"db": map[string]any{"answers": []string{"SQLite"}}}})
			decision := "decline"
			if f.Scenario == "allow" {
				decision = "accept"
			}
			p.callback("item/commandExecution/requestApproval", map[string]any{"threadId": testSessionID, "turnId": contractTurnID, "itemId": "command-one", "command": "go test ./...", "cwd": cwd}, map[string]any{"decision": decision})
		} else {
			option := "reject"
			if f.Scenario == "allow" {
				option = "allow"
			}
			p.callback("session/request_permission", map[string]any{"sessionId": testSessionID, "command": []string{"go", "test", "./..."}, "toolCall": map[string]any{"toolCallId": "command-one", "title": "Run tests"}, "options": []any{map[string]any{"optionId": "allow", "name": "Allow", "kind": "allow_once"}, map[string]any{"optionId": "reject", "name": "Reject", "kind": "reject_once"}}}, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": option}})
		}
	}
}

func (p *protocolPeer) finishFixtureTurn(f protocolFixture) {
	if f.Kind == "acp" {
		stop := "end_turn"
		if f.Scenario == "refusal" {
			stop = "refusal"
		}
		p.reply(map[string]any{"stopReason": stop})
	} else {
		status := "completed"
		var failure any
		switch f.Scenario {
		case "failed":
			status = "failed"
			failure = map[string]any{"message": "fixture turn failed", "codexErrorInfo": "unauthorized"}
		case "interrupted":
			status = "interrupted"
		}
		p.notify("turn/completed", map[string]any{"threadId": testSessionID, "turn": map[string]any{"id": contractTurnID, "items": []any{}, "status": status, "error": failure}})
	}
}

func TestNativeThinkingUsesDiscoveryACPCommand(t *testing.T) {
	for _, kind := range []string{"codex", "claude"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			adapter := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: "thinking", Model: contractSelectedModel})
			adapter.Provider.ModelCommand = adapter.Provider.Command
			adapter.Provider.Command = []string{"must-not-execute-native-command"}
			adapter.Provider.Kind = kind
			out, err := adapter.Run(ctx, Input{CWD: t.TempDir(), Prompt: contractPrompt, Model: contractSelectedModel, Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}}, Hooks{})
			if err != nil || out.Text != contractReply || out.ResolvedModel != contractSelectedModel {
				t.Fatalf("model response: %+v %v", out, err)
			}
			if out.EffectiveThinking == nil || out.EffectiveThinking.Value != "high" {
				t.Fatalf("native thinking did not use ACP: %+v %v", out, err)
			}
		})
	}
}
