package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"whirlwind/internal/model"
)

const contractPrompt = "deliver café 👋\nsecond line"
const contractReply = "received café 👋\nsecond line"

const (
	contractModelConfig       = "model-config"
	contractModelLegacy       = "model-legacy"
	contractSelectedModel     = "selected-model"
	contractThinkingCatalog   = "thinking-catalog"
	contractCatalogConfig     = "catalog-config"
	contractCatalogLegacy     = "catalog-legacy"
	contractSelectedModelName = "Selected model"
	contractModelOptionID     = "provider-model"

	contractNoLoad       = "no-load"
	contractEmptyLoad    = "load-empty-result"
	contractInitError    = "initialize-error"
	contractAuthError    = "authentication required"
	contractEmptySession = "empty-session"
	contractTurnID       = "turn-one"
)

type protocolFixture struct {
	Kind     string
	Scenario string
	Resume   bool
	ReadOnly bool
	Model    string
}

func TestExplicitModelSelection(t *testing.T) {
	for _, tc := range []struct{ kind, scenario string }{{"codex", "model"}, {"acp", contractModelConfig}, {"acp", contractModelLegacy}, {"acp", "model-unsupported"}} {
		t.Run(tc.kind+"/"+tc.scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := contractAdapter(t, protocolFixture{Kind: tc.kind, Scenario: tc.scenario, Model: contractSelectedModel}).Run(ctx, Input{CWD: t.TempDir(), Prompt: contractPrompt, Model: contractSelectedModel}, Hooks{})
			if tc.scenario == "model-unsupported" {
				if err == nil || !strings.Contains(err.Error(), "model selection") {
					t.Fatalf("model silently ignored: %v", err)
				}
				return
			}
			if err != nil || out.Text != contractReply {
				t.Fatalf("selected model round trip: %+v %v", out, err)
			}
		})
	}
}

func TestACPThinkingAppliesAfterModelForNewAndResumedSessions(t *testing.T) {
	for _, resume := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		input := Input{CWD: t.TempDir(), Prompt: contractPrompt, Model: contractSelectedModel}
		_ = json.Unmarshal([]byte(`{"Thinking":{"config_id":"effort","value":"high"}}`), &input)
		if resume {
			input.SessionID = testSessionID
		}
		out, err := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: "thinking", Model: contractSelectedModel, Resume: resume}).Run(ctx, input, Hooks{})
		cancel()
		if err != nil || out.Text != contractReply {
			t.Fatalf("resume=%v: %+v %v", resume, out, err)
		}
	}
}
func TestThinkingCatalogUsesModelResponseWithoutInference(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adapter := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: contractThinkingCatalog, Model: contractSelectedModel, ReadOnly: true})
	catalog, err := Models(ctx, adapter.Provider, t.TempDir(), contractSelectedModel)
	if err != nil || catalog.Current != contractSelectedModel || len(catalog.ThinkingOptions) != 1 || catalog.ThinkingOptions[0].ConfigID != "effort" || len(catalog.ThinkingOptions[0].Options) != 1 || catalog.ThinkingOptions[0].Options[0].ID != "high" {
		t.Fatalf("model-dependent thinking lost: %+v %v", catalog, err)
	}
}

func TestUnsupportedACPThinkingFailsBeforePrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: contractModelConfig, Model: contractSelectedModel}).Run(ctx, Input{CWD: t.TempDir(), Prompt: contractPrompt, Model: contractSelectedModel, Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}}, Hooks{})
	if err == nil || !strings.Contains(err.Error(), "not advertised") || out.Text != "" {
		t.Fatalf("thinking silently ignored: %+v %v", out, err)
	}
}

func TestModelCatalogUsesProviderMetadataWithoutPrompt(t *testing.T) {
	for _, tc := range []struct{ kind, scenario string }{{"codex", contractCatalogConfig}, {"claude", contractCatalogLegacy}, {"acp", contractCatalogConfig}, {"acp", contractCatalogLegacy}} {
		t.Run(tc.kind+"/"+tc.scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			adapter := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: tc.scenario, ReadOnly: true})
			if tc.kind != "acp" {
				adapter.Provider.ModelCommand = adapter.Provider.Command
				adapter.Provider.Command = []string{"must-not-use-native-model-discovery"}
				adapter.Provider.Kind = tc.kind
			}
			catalog, err := Models(ctx, adapter.Provider, t.TempDir())
			if err != nil || len(catalog.Models) != 1 || catalog.Models[0].ID != contractSelectedModel || catalog.Models[0].Name != contractSelectedModelName {
				t.Fatalf("catalog lost: %+v %v", catalog, err)
			}
		})
	}
}

func contractAdapter(t *testing.T, fixture protocolFixture) ProcessAdapter {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return ProcessAdapter{Provider: model.Provider{Kind: fixture.Kind, Command: []string{exe, "-test.run=^TestProtocolContractHelper$", "--", string(encoded)}}}
}

// Rejecting/reordering outgoing messages or losing streamed chunks breaks this test.
func TestProtocolMessageContracts(t *testing.T) {
	for _, kind := range []string{"acp", "codex"} {
		for _, resume := range []bool{false, true} {
			for _, readOnly := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/resume=%t/readOnly=%t", kind, resume, readOnly), func(t *testing.T) {
					verifyProtocolMessages(t, protocolFixture{Kind: kind, Resume: resume, ReadOnly: readOnly})
				})
			}
		}
	}
}

func verifyProtocolMessages(t *testing.T, fixture protocolFixture) {
	t.Helper()
	input := Input{CWD: t.TempDir(), Prompt: contractPrompt, ReadOnly: fixture.ReadOnly}
	if fixture.Resume {
		input.SessionID = testSessionID
	}
	var mu sync.Mutex
	var events []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := contractAdapter(t, fixture).Run(ctx, input, Hooks{Event: func(kind string, _ any) {
		mu.Lock()
		events = append(events, kind)
		mu.Unlock()
	}})
	if err != nil || out.SessionID != testSessionID || out.Text != contractReply {
		t.Fatalf("message round trip: %+v, %v", out, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) < 3 {
		t.Fatalf("missing session/message events: %v", events)
	}
}

func TestACPResumeExcludesReplayedHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: "replay", Resume: true}).Run(ctx,
		Input{CWD: t.TempDir(), SessionID: testSessionID, Prompt: contractPrompt}, Hooks{})
	if err != nil || out.Text != contractReply {
		t.Fatalf("current response includes history: %+v, %v", out, err)
	}
}

func TestACPResumeCapabilityAndEmptyLoadResult(t *testing.T) {
	for _, scenario := range []string{contractNoLoad, contractEmptyLoad} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: scenario, Resume: true}).Run(ctx,
				Input{CWD: t.TempDir(), SessionID: testSessionID, Prompt: contractPrompt}, Hooks{})
			if err != nil || out.SessionID != testSessionID || out.Text != contractReply {
				t.Fatalf("resume capability/load response mishandled: %+v, %v", out, err)
			}
		})
	}
}

func TestProtocolFailures(t *testing.T) {
	cases := []struct{ kind, scenario, want string }{
		{"acp", contractInitError, contractAuthError},
		{"codex", contractInitError, contractAuthError},
		{"acp", "incompatible-version", "protocol version"},
		{"acp", contractEmptySession, "ACP returned empty session ID"},
		{"codex", contractEmptySession, "codex returned empty thread ID"},
		{"acp", "refusal", "refusal"},
		{"codex", "failed", "fixture turn failed"},
		{"codex", "interrupted", "interrupted"},
		{"acp", "malformed", "malformed provider frame"},
		{"codex", "malformed", "malformed provider frame"},
		{"acp", "exit", "exited"},
		{"codex", "exit", "exited"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := contractAdapter(t, protocolFixture{Kind: tc.kind, Scenario: tc.scenario}).Run(ctx,
				Input{CWD: t.TempDir(), Prompt: contractPrompt}, Hooks{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestProtocolCancellationDuringPrompt(t *testing.T) {
	for _, kind := range []string{"acp", "codex"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := contractAdapter(t, protocolFixture{Kind: kind, Scenario: "stall"}).Run(ctx,
				Input{CWD: t.TempDir(), Prompt: contractPrompt}, Hooks{Event: func(kind string, _ any) {
					if kind == "fixture/prompt" {
						cancel()
					}
				}})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestProtocolCallbacks(t *testing.T) {
	for _, kind := range []string{"acp", "codex"} {
		for _, allow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/allow=%t", kind, allow), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				verifyProtocolCallbacks(t, ctx, kind, allow)
			})
		}
	}
}

func verifyProtocolCallbacks(t *testing.T, ctx context.Context, kind string, allow bool) {
	t.Helper()
	scenario := "deny"
	if allow {
		scenario = "allow"
	}
	out, err := contractAdapter(t, protocolFixture{Kind: kind, Scenario: scenario}).Run(ctx,
		Input{CWD: t.TempDir(), Prompt: contractPrompt}, Hooks{
			Permission: func(_ context.Context, p Permission) (bool, error) {
				if !reflect.DeepEqual(p.Command, []string{"go", "test", "./..."}) {
					return false, fmt.Errorf("command lost: %v", p.Command)
				}
				return allow, nil
			},
			Questions: func(_ context.Context, questions []model.Question) (map[string]string, error) {
				if len(questions) != 1 || questions[0].ID != "db" || questions[0].Text != "Which database?" || !reflect.DeepEqual(questions[0].Options, []string{"SQLite", "PostgreSQL"}) {
					return nil, fmt.Errorf("question lost: %+v", questions)
				}
				return map[string]string{"db": "SQLite"}, nil
			},
		})
	if err != nil || out.Text != contractReply {
		t.Fatalf("callback round trip: %+v, %v", out, err)
	}
}

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
			if err != nil || out.Text != contractReply || out.ResolvedModel != contractSelectedModel || out.EffectiveThinking == nil || out.EffectiveThinking.Value != "high" {
				t.Fatalf("native thinking did not use ACP: %+v %v", out, err)
			}
		})
	}
}
