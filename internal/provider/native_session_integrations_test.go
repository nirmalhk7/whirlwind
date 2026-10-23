package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
	"whirlwind/internal/model"
)

const (
	nativeFixtureSavedSession = "saved-session"
	nativeFixtureFinalThread  = "final-thread"
	nativeFixtureProbeThread  = "probe-thread"
)

func TestNativeCodexStartsColdThreadWithPolicyBeforeInference(t *testing.T) {
	for _, saved := range []string{"", nativeFixtureSavedSession} {
		for _, readOnly := range []bool{true, false} {
			t.Run(fmt.Sprintf("saved=%t/readonly=%t", saved != "", readOnly), func(t *testing.T) {
				s, close := nativeSessionFixture(t, saved, readOnly, false)
				defer close()
				if err := s.runCodex(); err != nil {
					t.Fatal(err)
				}
				if s.output.SessionID != nativeFixtureFinalThread || s.output.ResolvedModel != "resolved-model" {
					t.Fatalf("wrong final thread state: %+v", s.output)
				}
			})
		}
	}
}

func TestNativeCodexCatalogProbeDoesNotPublishResumableSession(t *testing.T) {
	s, close := nativeSessionFixture(t, nativeFixtureSavedSession, true, true)
	defer close()
	if err := s.runCodex(); err != nil {
		t.Fatal(err)
	}
	if len(s.output.Integrations) != 1 || s.output.SessionID != "" {
		t.Fatalf("catalog probe became execution session: %+v", s.output)
	}
}

func TestNativeCodexFailedProbeDoesNotRetainSavedSession(t *testing.T) {
	s, close := nativeSessionFixture(t, nativeFixtureSavedSession, true, false)
	defer close()
	s.input.RequiredIntegrations = []model.IntegrationTarget{{Provider: "codex", Ref: "mcp://missing"}}
	if err := s.runCodex(); err == nil {
		t.Fatal("missing required integration accepted")
	}
	if s.output.SessionID != "" {
		t.Fatal("native failure retained an unprotected saved session")
	}
}

func nativeSessionFixture(t *testing.T, saved string, readOnly, catalogOnly bool) (*providerSession, func()) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	role := "code"
	if readOnly {
		role = "review"
	}
	s := &providerSession{ctx: ctx, input: Input{NativeIntegrations: true, ProviderID: "codex", SessionID: saved, ReadOnly: readOnly, CatalogOnly: catalogOnly, CWD: t.TempDir(), Model: "selected-model", Role: role, Prompt: "inspect native CI"}, output: Output{SessionID: saved}, completed: make(chan string, 1), hooks: Hooks{RunCheck: func(context.Context, string) (CheckResult, error) { return CheckResult{}, nil }}}
	s.hooks.Event = func(kind string, value any) {
		if kind != "whirlwind/session" {
			return
		}
		event, _ := value.(map[string]any)
		if event["session_id"] != nativeFixtureFinalThread || event["resumed"] != false || catalogOnly {
			t.Errorf("probe/stale session published: %+v", event)
		}
	}
	rpc, err := Start(ctx, []string{exe, "-test.run=^TestNativeColdThreadRPCFixture$", "--", "native-cold-thread", fmt.Sprint(readOnly)}, s.input.CWD, s.handle)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	s.rpc = rpc
	return s, func() { rpc.Close(); cancel() }
}

func TestNativeColdThreadRPCFixture(t *testing.T) {
	index := slices.Index(os.Args, "native-cold-thread")
	if index < 0 {
		return
	}
	fixture := nativeColdThreadFixture{
		readOnly: os.Args[index+1] == "true",
		write:    func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) },
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var frame Message
		_ = json.Unmarshal(scanner.Bytes(), &frame)
		if frame.Method == "initialized" {
			continue
		}
		fixture.respond(frame)
	}
	os.Exit(0)
}

type nativeColdThreadFixture struct {
	readOnly bool
	starts   int
	write    func(any)
}

func (fixture *nativeColdThreadFixture) respond(frame Message) {
	var params map[string]any
	_ = json.Unmarshal(frame.Params, &params)
	result, failure := fixture.handle(frame.Method, params)
	if failure != "" {
		fixture.write(map[string]any{"id": frame.ID, "error": map[string]any{"code": -32000, "message": failure}})
		return
	}
	fixture.write(map[string]any{"id": frame.ID, "result": result})
}

func (fixture *nativeColdThreadFixture) handle(method string, params map[string]any) (any, string) {
	var result any = map[string]any{}
	failure := ""
	switch method {
	case "initialize", "account/rateLimits/read":
	case "thread/start":
		result, failure = fixture.start(params)
	case "thread/resume":
		failure = "native policy must never rely on loaded-thread resume overrides"
	case "app/list":
		result = map[string]any{"data": []any{}}
	case "app/installed":
		result = map[string]any{"apps": []any{}}
	case "mcpServerStatus/list":
		if params["threadId"] != nativeFixtureProbeThread || fixture.starts != 1 {
			failure = "discovery did not use isolated probe thread"
		}
		result = nativeFixtureMCPStatus()
	case "turn/start":
		failure = fixture.turn(params)
	default:
		failure = "unexpected protocol method " + method
	}
	return result, failure
}

func (fixture *nativeColdThreadFixture) start(params map[string]any) (any, string) {
	fixture.starts++
	id, failure := nativeFixtureProbeThread, ""
	if fixture.starts == 2 {
		id = nativeFixtureFinalThread
		failure = fixture.finalThreadFailure(params)
	}
	if fixture.starts > 2 {
		failure = "too many native threads"
	}
	return map[string]any{"thread": map[string]any{"id": id}, "model": "resolved-model"}, failure
}

func (fixture *nativeColdThreadFixture) finalThreadFailure(params map[string]any) string {
	config, _ := params["config"].(map[string]any)
	failure := fixture.nativeToolPolicyFailure(config)
	sandbox := "workspace-write"
	if fixture.readOnly {
		sandbox = "read-only"
	}
	tools, _ := params["dynamicTools"].([]any)
	if params["sandbox"] != sandbox || !nativeFixtureOriginalSettings(params) || len(tools) != 1 {
		failure = "final thread lost original sandbox, model, approvals, or checks"
	}
	return failure
}

func (fixture *nativeColdThreadFixture) nativeToolPolicyFailure(config map[string]any) string {
	failure := ""
	if config == nil {
		failure = "final cold thread lacks native tool policy"
	}
	if fixture.readOnly {
		allowed, _ := config["mcp_servers.ci.enabled_tools"].([]any)
		if len(allowed) != 1 || allowed[0] != "read_build" {
			failure = "unsafe native reads exposed"
		}
	} else if config["mcp_servers.ci.tools.delete_build.approval_mode"] != "prompt" {
		failure = "native write can bypass inbox"
	}
	return failure
}

func nativeFixtureOriginalSettings(params map[string]any) bool {
	return params["approvalPolicy"] == "untrusted" && params["approvalsReviewer"] == "user" && params["model"] == "selected-model"
}

func nativeFixtureMCPStatus() map[string]any {
	return map[string]any{"data": []any{map[string]any{"name": "ci", "runtimeStatus": "connected", "tools": map[string]any{"read_build": map[string]any{"name": "read_build", "annotations": map[string]any{"readOnlyHint": true}}, "delete_build": map[string]any{"name": "delete_build", "annotations": map[string]any{"destructiveHint": true}}}}}}
}

func (fixture *nativeColdThreadFixture) turn(params map[string]any) string {
	if params["threadId"] != nativeFixtureFinalThread || fixture.starts != 2 {
		return "inference started on unprotected probe thread"
	}
	fixture.write(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"status": "completed"}}})
	return ""
}
