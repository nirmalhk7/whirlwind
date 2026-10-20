package provider

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

func (p *protocolPeer) openFixtureSession(f protocolFixture) string {
	method := "session/new"
	if f.Resume && f.Scenario != contractNoLoad {
		method = "session/load"
	}
	if f.Kind == "codex" {
		method = "thread/start"
		if f.Resume {
			method = "thread/resume"
		}
	}
	params := p.read(method)
	cwd, _ := os.Getwd()
	requestedCWD, _ := params["cwd"].(string)
	canonicalCWD, cwdErr := filepath.EvalSymlinks(requestedCWD)
	p.require(cwdErr == nil && canonicalCWD == cwd, "working directory lost")
	if f.Resume && f.Scenario != contractNoLoad {
		key := "sessionId"
		if f.Kind == "codex" {
			key = "threadId"
		}
		p.require(params[key] == testSessionID, "resume ID lost")
	}
	id := testSessionID
	if f.Scenario == contractEmptySession {
		id = ""
	}
	p.fixtureSessionReply(f, params, id)
	return cwd
}

func (p *protocolPeer) fixtureSessionReply(f protocolFixture, params map[string]any, id string) {
	if f.Kind == "acp" {
		p.acpFixtureSessionReply(f, params, id)
		return
	}
	p.codexFixtureSessionReply(f, params, id)
}

func (p *protocolPeer) acpFixtureSessionReply(f protocolFixture, params map[string]any, id string) {
	p.require(reflect.DeepEqual(params["mcpServers"], []any{}), "mcpServers must be an array")
	p.replyACPFixtureMetadata(f, id)
	if strings.HasPrefix(f.Scenario, "catalog-") {
		for p.scanner.Scan() {
		}
		return
	}
	if f.ReadOnly && f.Scenario != contractThinkingCatalog {
		params = p.read("session/set_mode")
		p.require(params["sessionId"] == testSessionID && params["modeId"] == "ask", "read-only mode lost")
		p.reply(map[string]any{})
	}
	p.configureACPFixtureModel(f)
}

func (p *protocolPeer) replyACPFixtureMetadata(f protocolFixture, id string) {
	if f.Scenario == "replay" || f.Scenario == contractEmptyLoad {
		p.chunk("acp", "old assistant reply ")
	}
	if f.Scenario == contractEmptyLoad {
		p.reply(map[string]any{})
	} else {
		result := map[string]any{"sessionId": id, "modes": map[string]any{"currentModeId": "code", "availableModes": []any{map[string]any{"id": "ask", "name": "Ask"}, map[string]any{"id": "code", "name": "Code"}}}}
		if f.Scenario == contractModelConfig || f.Scenario == contractCatalogConfig || strings.HasPrefix(f.Scenario, "thinking") {
			result["configOptions"] = []any{map[string]any{"id": contractModelOptionID, "category": "model", "type": "select", "currentValue": "default-model", "options": []any{map[string]any{"value": contractSelectedModel, "name": contractSelectedModelName}}}}
		}
		if f.Scenario == contractModelLegacy || f.Scenario == contractCatalogLegacy {
			result["models"] = map[string]any{"currentModelId": "default-model", "availableModels": []any{map[string]any{"modelId": contractSelectedModel, "name": contractSelectedModelName}}}
		}
		p.reply(result)
	}
}

func (p *protocolPeer) configureACPFixtureModel(f protocolFixture) {
	var params map[string]any
	if f.Scenario == contractModelConfig || strings.HasPrefix(f.Scenario, "thinking") {
		params = p.read("session/set_config_option")
		p.require(params["sessionId"] == testSessionID && params["configId"] == contractModelOptionID && params["value"] == f.Model, "ACP selected model missing")
		if strings.HasPrefix(f.Scenario, "thinking") {
			p.configureACPFixtureThinking(f)
		} else {
			p.reply(map[string]any{})
		}
	}
	if f.Scenario == contractModelLegacy {
		params = p.read("session/set_model")
		p.require(params["sessionId"] == testSessionID && params["modelId"] == f.Model, "ACP selected model missing")
		p.reply(map[string]any{})
	}
}
