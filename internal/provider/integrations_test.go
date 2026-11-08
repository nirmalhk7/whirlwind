package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/model"
)

const (
	integrationSessionUpdate = "session/update"
	integrationCodeHostName  = "Code host"
	integrationCodeHostID    = "code-host"
)

func TestCodexCatalogRequiresInstalledCallableState(t *testing.T) {
	apps := json.RawMessage(`{"data":[{"id":"jenkins","name":"Jenkins","isEnabled":true,"isAccessible":true}]}`)
	installed := json.RawMessage(`{"apps":[{"id":"jenkins","enabled":true,"callable":false}]}`)
	got, err := codexAppCatalog("codex", apps, installed)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Callable {
		t.Fatalf("uncallable integration advertised: %+v", got)
	}
}

func nativeTestInventory() []model.Integration {
	return []model.Integration{{Provider: "cursor", Ref: "mcp://ci", Name: "ci", Kind: "mcp", Enabled: true, Callable: true, Tools: []model.IntegrationTool{{Name: "read_build", ReadOnly: true}, {Name: "delete_build"}}}}
}

func nativeUpdate(method, body string) Message {
	return Message{Method: method, Params: json.RawMessage(body)}
}

func TestNativeACPEvidenceJoinsInitialMetadataAndSuccessfulTerminalResult(t *testing.T) {
	s := &providerSession{input: Input{NativeIntegrations: true}, output: Output{Integrations: nativeTestInventory()}}
	s.captureIntegrationTool(nativeUpdate(integrationSessionUpdate, `{"update":{"sessionUpdate":"tool_call","toolCallId":"one","name":"mcp__ci__read_build","status":"pending"}}`))
	if !s.nativeReadPermission(map[string]any{"toolCall": map[string]any{"toolCallId": "one"}}) {
		t.Fatal("pending annotated read denied")
	}
	s.captureIntegrationTool(nativeUpdate(integrationSessionUpdate, `{"update":{"sessionUpdate":"tool_call_update","toolCallId":"one","status":"completed","rawOutput":{"content":[{"type":"text","text":"ok"}],"isError":false}}}`))
	want := []ToolEvidence{{Ref: "mcp://ci", Tool: "read_build", CallID: "one", Success: true}}
	if !reflect.DeepEqual(s.output.ToolCalls, want) {
		t.Fatalf("missing joined native evidence: %+v", s.output.ToolCalls)
	}
	// Replayed terminal updates must not produce a second publication witness.
	s.captureIntegrationTool(nativeUpdate(integrationSessionUpdate, `{"update":{"sessionUpdate":"tool_call_update","toolCallId":"one","status":"completed"}}`))
	if len(s.output.ToolCalls) != 1 {
		t.Fatal("duplicate completion accepted")
	}
}

func TestNativeEvidenceRejectsToolFailuresAndAmbiguousNames(t *testing.T) {
	for _, raw := range []string{
		`{"update":{"sessionUpdate":"tool_call_update","toolCallId":"one","status":"completed","rawOutput":{"isError":true}}}`,
		`{"update":{"sessionUpdate":"tool_call_update","toolCallId":"one","status":"failed"}}`,
		`{"update":{"sessionUpdate":"tool_call_update","toolCallId":"one","status":"completed","error":{"message":"failed"}}}`,
	} {
		s := &providerSession{input: Input{NativeIntegrations: true}, output: Output{Integrations: nativeTestInventory()}}
		s.captureIntegrationTool(nativeUpdate(integrationSessionUpdate, `{"update":{"sessionUpdate":"tool_call","toolCallId":"one","name":"mcp__ci__read_build","status":"pending"}}`))
		s.captureIntegrationTool(nativeUpdate(integrationSessionUpdate, raw))
		if len(s.output.ToolCalls) != 1 || s.output.ToolCalls[0].Success {
			t.Fatalf("failure witness lost or became success: %+v", s.output.ToolCalls)
		}
	}
	s := &providerSession{input: Input{NativeIntegrations: true}, output: Output{Integrations: append(nativeTestInventory(), model.Integration{Ref: "mcp://other", Kind: "mcp", Name: "other", Enabled: true, Callable: true, Tools: []model.IntegrationTool{{Name: "read_build", ReadOnly: true}}})}}
	s.captureIntegrationTool(nativeUpdate(integrationSessionUpdate, `{"update":{"sessionUpdate":"tool_call","toolCallId":"ambiguous","name":"read_build","status":"pending"}}`))
	s.captureIntegrationTool(nativeUpdate(integrationSessionUpdate, `{"update":{"sessionUpdate":"tool_call_update","toolCallId":"ambiguous","status":"completed"}}`))
	if len(s.output.ToolCalls) != 0 || s.nativeReadPermission(map[string]any{"toolCall": map[string]any{"toolCallId": "ambiguous"}}) {
		t.Fatal("ambiguous native name authorized")
	}
}

func TestNativeReadPermissionRequiresExactCallableReadAnnotation(t *testing.T) {
	s := &providerSession{input: Input{NativeIntegrations: true}, output: Output{Integrations: nativeTestInventory()}}
	for _, test := range []struct {
		server, tool string
		want         bool
	}{{"ci", "read_build", true}, {"ci", "delete_build", false}, {"ci", "unknown", false}, {"missing", "read_build", false}} {
		if got := s.nativeReadPermission(map[string]any{"server": test.server, "tool": test.tool}); got != test.want {
			t.Fatalf("%s/%s permission %v", test.server, test.tool, got)
		}
	}
	s.output.Integrations[0].Callable = false
	if s.nativeReadPermission(map[string]any{"server": "ci", "tool": "read_build"}) {
		t.Fatal("disconnected native read authorized")
	}
}

func TestNativePolicyRestrictsReadOnlyToolsAndPromptsForWrites(t *testing.T) {
	catalog := append(nativeTestInventory(), model.Integration{Ref: "app://code-host", Name: integrationCodeHostName, Kind: "app", Enabled: true, Callable: true, Tools: []model.IntegrationTool{{Name: "repos/list", ReadOnly: true}, {Name: "pulls/create"}}})
	s := &providerSession{input: Input{ReadOnly: true}}
	policy := s.nativeIntegrationPolicy(catalog)
	if !reflect.DeepEqual(policy["mcp_servers.ci.enabled_tools"], []string{"read_build"}) {
		t.Fatalf("native writes exposed: %+v", policy)
	}
	if policy["mcp_servers.ci.tools.read_build.approval_mode"] != "auto" {
		t.Fatal("positively annotated native read still needs elicitation")
	}
	if policy["apps.code-host.default_tools_enabled"] != false || policy["apps.code-host.tools.\"repos/list\".enabled"] != true || policy["apps.code-host.tools.\"pulls/create\".enabled"] != false {
		t.Fatalf("unsafe app tool policy: %+v", policy)
	}
	s.input.ReadOnly = false
	policy = s.nativeIntegrationPolicy(catalog)
	if policy["mcp_servers.ci.default_tools_approval_mode"] != "prompt" || policy["mcp_servers.ci.tools.delete_build.approval_mode"] != "prompt" || policy["apps.code-host.tools.\"pulls/create\".approval_mode"] != "prompt" {
		t.Fatalf("native writes bypass inbox: %+v", policy)
	}
}

func TestCodexNativeDiscoveryReadsAppToolMetadata(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rpc, err := Start(ctx, []string{exe, "-test.run=^TestNativeCatalogRPCFixture$", "--", "native-catalog"}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := rpc.Close(); err != nil {
			t.Errorf("close native catalog client: %v", err)
		}
	})
	s := &providerSession{ctx: ctx, rpc: rpc, input: Input{ProviderID: "codex"}}
	catalog, err := s.discoverCodexIntegrations("thread-native")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 || catalog[0].Name != integrationCodeHostName || len(catalog[0].Tools) != 2 || !catalog[0].Tools[0].ReadOnly || catalog[0].Tools[1].ReadOnly {
		t.Fatalf("missing app summaries: %+v", catalog)
	}
}

func TestNativeCatalogRPCFixture(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "native-catalog") {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var frame Message
		_ = json.Unmarshal(scanner.Bytes(), &frame)
		var result any
		switch frame.Method {
		case "app/list":
			result = map[string]any{"data": []any{map[string]any{"id": integrationCodeHostID, "name": integrationCodeHostName}}}
		case "app/installed":
			result = map[string]any{"apps": []any{map[string]any{"id": integrationCodeHostID, "enabled": true, "callable": true}}}
		case "app/read":
			var params map[string]any
			_ = json.Unmarshal(frame.Params, &params)
			if params["threadId"] != "thread-native" || params["includeTools"] != true {
				os.Exit(9)
			}
			result = map[string]any{"apps": []any{map[string]any{"id": integrationCodeHostID, "name": integrationCodeHostName, "toolSummaries": []any{map[string]any{"name": "repos/list", "description": "List repos", "isEnabled": true, "isReadOnly": true}, map[string]any{"name": "pulls/create", "description": "Create PR", "isEnabled": true, "isReadOnly": false}}}}, "missingAppIds": []any{}}
		case "mcpServerStatus/list":
			result = map[string]any{"data": []any{}}
		default:
			os.Exit(10)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": frame.ID, "result": result})
	}
	os.Exit(0)
}
func TestNativeReadToolDoesNotTrustToolName(t *testing.T) {
	if integrationToolReadOnly("mcp__jenkins__delete_job", nil) {
		t.Fatal("unknown tool granted read access")
	}
	if !integrationToolReadOnly("mcp__jenkins__build", map[string]any{"readOnlyHint": true}) {
		t.Fatal("read annotation ignored")
	}
}
