package provider

import (
	"bufio"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Acts only as the external agent boundary; the parent runs real Go transport and adapter.
func TestProtocolContractHelper(t *testing.T) {
	for n, arg := range os.Args {
		if arg == "--" && n+1 < len(os.Args) {
			var fixture protocolFixture
			if err := json.Unmarshal([]byte(os.Args[n+1]), &fixture); err != nil {
				os.Exit(2)
			}
			runProtocolFixture(fixture)
			os.Exit(0)
		}
	}
}

type protocolPeer struct {
	scanner *bufio.Scanner
	message Message
}

func (p *protocolPeer) emit(value any) {
	if json.NewEncoder(os.Stdout).Encode(value) != nil {
		os.Exit(2)
	}
}

func (p *protocolPeer) require(ok bool, detail string) {
	if !ok {
		p.emit(map[string]any{"jsonrpc": "2.0", "id": p.message.ID, "error": map[string]any{"code": -32602, "message": detail}})
		os.Exit(0)
	}
}

func (p *protocolPeer) read(method string) map[string]any {
	if !p.scanner.Scan() {
		os.Exit(2)
	}
	p.message = Message{}
	p.require(json.Unmarshal(p.scanner.Bytes(), &p.message) == nil, "invalid request JSON")
	p.require(p.message.JSONRPC == "2.0" && p.message.Method == method, "expected "+method+", got "+p.message.Method)
	var params map[string]any
	p.require(json.Unmarshal(p.message.Params, &params) == nil, "invalid params")
	return params
}

func (p *protocolPeer) reply(value any) {
	p.emit(map[string]any{"jsonrpc": "2.0", "id": p.message.ID, "result": value})
}

func (p *protocolPeer) notify(method string, params any) {
	p.emit(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (p *protocolPeer) chunk(kind, text string) {
	if kind == "acp" {
		p.notify("session/update", map[string]any{"sessionId": testSessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}})
	} else {
		p.notify("item/agentMessage/delta", map[string]any{"threadId": testSessionID, "turnId": contractTurnID, "itemId": "message-one", "delta": text})
	}
}

func (p *protocolPeer) callback(method string, params, want any) {
	id := json.RawMessage(`"callback-one"`)
	p.emit(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if !p.scanner.Scan() {
		os.Exit(2)
	}
	var response Message
	p.require(json.Unmarshal(p.scanner.Bytes(), &response) == nil && string(response.ID) == string(id), "callback response ID lost")
	var result any
	p.require(json.Unmarshal(response.Result, &result) == nil, "callback failed: "+string(response.Error))
	expected, _ := json.Marshal(want)
	var decoded any
	_ = json.Unmarshal(expected, &decoded)
	p.require(reflect.DeepEqual(result, decoded), "incorrect callback response: "+string(response.Result))
}

func runProtocolFixture(f protocolFixture) {
	p := &protocolPeer{scanner: bufio.NewScanner(os.Stdin)}
	p.initializeFixture(f)
	cwd := p.openFixtureSession(f)
	p.readFixturePrompt(f)
	if p.stopFixturePrompt(f) {
		return
	}
	p.fixtureCallbacks(f, cwd)
	for _, text := range []string{"received ", "café ", "👋\n", "second line"} {
		p.chunk(f.Kind, text)
	}
	p.finishFixtureTurn(f)
	for p.scanner.Scan() {
	}
}

func (p *protocolPeer) initializeFixture(f protocolFixture) {
	params := p.read("initialize")
	client, _ := params["clientInfo"].(map[string]any)
	p.require(client["name"] == "whirlwind" && client["version"] != "", "client identity missing")
	if f.Scenario == contractInitError {
		p.require(false, contractAuthError)
	}
	if f.Kind == "acp" {
		p.require(params["protocolVersion"] == float64(1), "wrong ACP protocol version")
		caps, _ := params["clientCapabilities"].(map[string]any)
		fs, _ := caps["fs"].(map[string]any)
		p.require(fs["readTextFile"] == true && fs["writeTextFile"] == !f.ReadOnly && caps["terminal"] == !f.ReadOnly, "incorrect read-only capabilities")
		version := 1
		if f.Scenario == "incompatible-version" {
			version = 99
		}
		p.reply(map[string]any{"protocolVersion": version, "agentCapabilities": map[string]any{"loadSession": f.Scenario != contractNoLoad}, "authMethods": []any{}})
	} else {
		p.reply(map[string]any{"userAgent": "codex-cli/0.160.0", "platformFamily": "unix", "platformOs": "macos"})
		p.read("initialized")
		p.require(len(p.message.ID) == 0, "initialized must be a notification")
		if f.Scenario == "catalog" {
			p.read("model/list")
			p.reply(map[string]any{"data": []any{map[string]any{"id": "catalog-id", "model": contractSelectedModel, "displayName": contractSelectedModelName}}, "nextCursor": nil})
			for p.scanner.Scan() {
			}
			return
		}
		p.read("account/rateLimits/read")
		p.reply(map[string]any{"rateLimits": nil, "rateLimitsByLimitId": nil})
	}
}
