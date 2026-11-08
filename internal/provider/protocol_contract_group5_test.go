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
	if f.Scenario == "failed" {
		p.emit(map[string]any{"jsonrpc": "2.0", "id": p.message.ID, "error": map[string]any{"code": -32000, "message": "fixture turn failed"}})
	}
}

func (p *protocolPeer) stopFixturePrompt(f protocolFixture) bool {
	if f.Scenario == "malformed" || f.Scenario == "failed" {
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
	stop := "end_turn"
	if f.Scenario == "refusal" {
		stop = "refusal"
	} else if f.Scenario == "interrupted" {
		stop = "interrupted"
	}
	p.reply(map[string]any{"stopReason": stop})
}

func TestNativeThinkingUsesDiscoveryACPCommand(t *testing.T) {
	for _, profile := range []string{"codex", "claude"} {
		t.Run(profile, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			adapter := contractAdapter(t, protocolFixture{Kind: "acp", ACPProfile: profile, Scenario: "thinking", Model: contractSelectedModel})
			adapter.Provider.ModelCommand = []string{"must-not-execute-native-command"}
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
