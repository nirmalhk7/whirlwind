package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/testutil/openshell"
)

func TestCursorMCPServerPreservesTransportAndCredentials(t *testing.T) {
	for _, test := range []struct {
		name, raw, want string
	}{
		{"ci", `{"command":"node","args":["ci.mjs"],"env":{"TOKEN":"secret","REGION":"west"}}`, `{"name":"ci","command":"node","args":["ci.mjs"],"env":[{"name":"REGION","value":"west"},{"name":"TOKEN","value":"secret"}]}`},
		{"remote", `{"url":"https://ci.internal/mcp","headers":{"Authorization":"Bearer secret"}}`, `{"type":"http","name":"remote","url":"https://ci.internal/mcp","headers":[{"name":"Authorization","value":"Bearer secret"}]}`},
		{"legacy", `{"type":"sse","url":"https://ci.internal/sse"}`, `{"type":"sse","name":"legacy","url":"https://ci.internal/sse","headers":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := cursorMCPServer(test.name, json.RawMessage(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(got)
			var actual, want any
			_ = json.Unmarshal(data, &actual)
			_ = json.Unmarshal([]byte(test.want), &want)
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("transport changed: %s", data)
			}
		})
	}
}

func TestCursorMCPServerRejectsUnsupportedDefinitionsWithoutLeaking(t *testing.T) {
	for _, raw := range []string{`null`, `{"command":"","env":{"TOKEN":"secret"}}`, `{"type":"websocket","url":"https://secret.internal"}`, `{"command":"node","url":"https://secret.internal"}`, `{"url":"file:///secret"}`} {
		if _, err := cursorMCPServer("ci", json.RawMessage(raw)); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("expected sanitized setup error for unsupported transport: %v", err)
		}
	}
}

func TestCursorMCPServerDoesNotSilentlyDiscardNativeAuthentication(t *testing.T) {
	for _, raw := range []string{`{"url":"https://ci.internal/mcp","auth":{"clientSecret":"secret"}}`, `{"command":"node","envFile":"/private/secret.env"}`} {
		if _, err := cursorMCPServer("ci", json.RawMessage(raw)); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("native configuration silently lost or exposed: %v", err)
		}
	}
}

func TestCursorListToolsCommandPreservesNativeFlags(t *testing.T) {
	got, err := cursorListToolsCommand([]string{"agent", "--profile", "work", "acp", "--unused"}, "ci")
	if err != nil || !reflect.DeepEqual(got, []string{"agent", "--profile", "work", "mcp", "list-tools", "ci"}) {
		t.Fatalf("wrong native command: %v %v", got, err)
	}
	if _, err := cursorListToolsCommand([]string{"agent"}, "ci"); err == nil {
		t.Fatal("ACP command without acp accepted")
	}
}

func TestCursorToolCatalogRequiresReadOnlyAnnotations(t *testing.T) {
	for _, raw := range []string{
		`{"tools":[{"name":"read","description":"Read CI","annotations":{"readOnlyHint":true}},{"name":"delete","annotations":{"readOnlyHint":true,"destructiveHint":true}},{"name":"unknown"}]}`,
		`{"result":{"tools":[{"name":"read","description":"Read CI","annotations":{"readOnlyHint":true}},{"name":"delete","annotations":{"readOnlyHint":true,"destructiveHint":true}},{"name":"unknown"}]}}`,
	} {
		got, err := cursorToolCatalog([]byte(raw))
		if err != nil || len(got) != 3 {
			t.Fatalf("catalog unavailable: %+v %v", got, err)
		}
		if got[0].Name != "read" || got[0].Description != "Read CI" || !got[0].ReadOnly || got[1].ReadOnly || got[2].ReadOnly {
			t.Fatalf("unsafe tool annotations: %+v", got)
		}
	}
	if got, err := cursorToolCatalog([]byte("Available tools:\nread-ci")); err != nil || len(got) != 0 {
		t.Fatalf("human output must not invent annotations: %+v %v", got, err)
	}
	if _, err := cursorToolCatalog([]byte(`{"error":{"message":"secret-token"}}`)); err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("native failure leaked or accepted: %v", err)
	}
}

func TestCursorIntegrationsMissingConnectionHasNativeSetupGuidance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := &providerSession{ctx: context.Background(), input: Input{ProviderID: "cursor", CWD: t.TempDir(), RequiredIntegrations: []model.IntegrationTarget{{Provider: "cursor", Ref: "mcp://dashboard-only"}}}}
	err := s.prepareCursorIntegrations(model.Provider{ID: "cursor", Kind: "acp", Command: []string{"agent", "acp"}}, execution.Profile{})
	if err == nil || !strings.Contains(err.Error(), "MCP configuration") {
		t.Fatalf("missing native setup guidance: %v", err)
	}
}

func TestCursorIntegrationsProbeRunsNativeCLIInsideSandbox(t *testing.T) {
	fixture := installProtocolOpenShell(t)
	fixture.EnableCommandExecution()
	project := t.TempDir()
	provider := model.Provider{ID: "cursor", Kind: "acp", ACPProfile: "cursor"}
	home := os.Getenv("HOME")
	writeNativeFixture(t, filepath.Join(home, ".cursor/mcp.json"), `{"mcpServers":{"ci":{"command":"node","args":["ci.mjs"]}}}`)
	command := filepath.Join(project, "agent-fixture")
	writeNativeFixture(t, command, `#!/bin/sh
if [ "$(cat "$HOME/.cursor/cli-config.json")" != 'synthetic test credential' ]; then exit 13; fi
if [ -e "$HOME/.codex/auth.json" ] || [ -e "$CLAUDE_CONFIG_DIR/.credentials.json" ]; then exit 14; fi
if [ "$1" = mcp ] && [ "$2" = list-tools ] && [ "$3" = ci ]; then
  printf '%s' '{"tools":[{"name":"read_ci","annotations":{"readOnlyHint":true}}]}'
  exit 0
fi
if [ "$1" = acp ]; then
  exec node -e 'const readline=require("node:readline");readline.createInterface({input:process.stdin}).on("line",line=>{const request=JSON.parse(line);if(request.id===undefined)return;const result=request.method==="initialize"?{protocolVersion:1,agentCapabilities:{loadSession:false},authMethods:[]}:request.method==="session/new"?{sessionId:"cursor-session"}:{};process.stdout.write(JSON.stringify({jsonrpc:"2.0",id:request.id,result})+"\n")});'
fi
exit 12
`)
	if err := os.Chmod(command, 0700); err != nil {
		t.Fatal(err)
	}
	provider.Command = []string{command, "acp"}
	profile, registryDir := cursorIntegrationTestProfile(t, project, provider)
	s := &providerSession{ctx: context.Background(), input: Input{ProviderID: "cursor", CWD: project, DiscoverConnected: true}}
	if err := s.prepareCursorIntegrations(provider, profile); err != nil {
		t.Fatal(err)
	}
	if len(s.output.Integrations) != 1 || !s.output.Integrations[0].Callable || len(s.output.Integrations[0].Tools) != 1 || !s.output.Integrations[0].Tools[0].ReadOnly {
		t.Fatalf("native inventory missing: %+v", s.output.Integrations)
	}
	if len(s.nativeServers) != 1 {
		t.Fatalf("native transport missing: %+v", s.nativeServers)
	}
	rpc, err := StartWithProfile(context.Background(), provider.Command, profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkACPSession(context.Background(), rpc, profile.CWD); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Close(); err != nil {
		t.Fatal(err)
	}
	assertCursorProbeCleanup(t, registryDir)
}

func TestCursorProbeUsesDistinctSandboxAndPreservesMainRuntime(t *testing.T) {
	fixture := installProtocolOpenShell(t)
	makeCursorSandboxIDsDistinct(t, fixture)
	project := t.TempDir()
	provider := model.Provider{ID: "cursor", Kind: "acp", ACPProfile: "cursor"}
	writeNativeFixture(t, filepath.Join(os.Getenv("HOME"), ".cursor/mcp.json"), `{"mcpServers":{"ci":{"command":"node","args":["ci.mjs"]}}}`)
	command := filepath.Join(project, "agent-fixture")
	writeNativeFixture(t, command, `#!/bin/sh
if [ "$1" = mcp ] && [ "$2" = list-tools ]; then
  printf '%s' '{"tools":[{"name":"read_ci","annotations":{"readOnlyHint":true}}]}'
  exit 0
fi
if [ "$1" = acp ]; then
  exec node -e 'const readline=require("node:readline");readline.createInterface({input:process.stdin}).on("line",line=>{const request=JSON.parse(line);if(request.id===undefined)return;const result=request.method==="initialize"?{protocolVersion:1,agentCapabilities:{loadSession:false},authMethods:[]}:request.method==="session/new"?{sessionId:"cursor-session"}:{};process.stdout.write(JSON.stringify({jsonrpc:"2.0",id:request.id,result})+"\n")});'
fi
exit 12
`)
	if err := os.Chmod(command, 0700); err != nil {
		t.Fatal(err)
	}
	provider.Command = []string{command, "acp"}
	profile, registryDir := cursorIntegrationTestProfile(t, project, provider)
	parentBefore, err := execution.ReadStandaloneRuntimes(registryDir)
	if err != nil || len(parentBefore) != 1 || parentBefore[0].ExecutionID != profile.ExecutionID || parentBefore[0].SandboxID != "" {
		t.Fatalf("main runtime was not initially registered without a sandbox ID: %+v %v", parentBefore, err)
	}
	session := &providerSession{ctx: context.Background(), input: Input{ProviderID: "cursor", CWD: project}}
	if err := session.prepareCursorIntegrations(provider, profile); err != nil {
		t.Fatal(err)
	}
	afterProbe, err := execution.ReadStandaloneRuntimes(registryDir)
	if err != nil || len(afterProbe) != 1 || afterProbe[0] != parentBefore[0] {
		t.Errorf("probe changed the main runtime locator: before=%+v after=%+v err=%v", parentBefore, afterProbe, err)
	}
	rpc, err := StartWithProfile(context.Background(), provider.Command, profile, nil)
	if err != nil {
		t.Fatalf("main ACP sandbox did not start after Cursor probe cleanup: %v", err)
	}
	if _, err := checkACPSession(context.Background(), rpc, profile.CWD); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Close(); err != nil {
		_, _ = fixture.RunCLI(t, "sandbox", "stop", profile.SandboxName, "--gateway", profile.GatewayName, "--gateway-endpoint", profile.GatewayEndpoint, "--workspace", profile.Workspace)
		_, _ = fixture.RunCLI(t, "sandbox", "delete", profile.SandboxName, "--gateway", profile.GatewayName, "--gateway-endpoint", profile.GatewayEndpoint, "--workspace", profile.Workspace)
		_ = completeStandaloneProfile(profile)
		t.Fatalf("main ACP sandbox cleanup failed after distinct probe resource: %v", err)
	}
	createdIDs := readCursorCreatedSandboxIDs(t, fixture)
	if len(createdIDs) != 2 || createdIDs[0] == createdIDs[1] {
		t.Fatalf("probe and main ACP must use distinct sandbox resource IDs: %v", createdIDs)
	}
	assertCursorProbeCleanup(t, registryDir)
	listed, err := fixture.RunCLI(t, "sandbox", "list", "--output", "json", "--gateway", "whirlwind-test-local", "--gateway-endpoint", "http://127.0.0.1:8080", "--workspace", "default")
	if err != nil || strings.Contains(string(listed), `"sandboxes":[{`) {
		t.Fatalf("Cursor probe or ACP sandbox remains after caller cleanup: %s %v", listed, err)
	}
}

func makeCursorSandboxIDsDistinct(t *testing.T, fixture *openshell.Fixture) {
	t.Helper()
	contents, err := os.ReadFile(fixture.Binary)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(string(contents), "id:'test-sandbox-' + name,", "id:'test-sandbox-' + name + '-' + process.pid,", 1)
	marker := "state.sandboxes[name] = sandboxValue(name);"
	if script == string(contents) || !strings.Contains(script, marker) {
		t.Fatal("fake OpenShell CLI resource identity markers are missing")
	}
	trace := "state.sandboxes[name] = sandboxValue(name);\n  fs.appendFileSync(statePath + '.created-ids', state.sandboxes[name].id + '\\n');"
	script = strings.Replace(script, marker, trace, 1)
	deleteMarker := "delete state.sandboxes[args[2]];"
	deleteFault := "state.delete_count=(state.delete_count??0)+1;\n  if(state.delete_count===1){writeState(state);process.stderr.write('controlled first-pass deletion fault\\n');process.exit(73);}\n  delete state.sandboxes[args[2]];"
	if !strings.Contains(script, deleteMarker) {
		t.Fatal("fake OpenShell delete marker is missing")
	}
	script = strings.Replace(script, deleteMarker, deleteFault, 1)
	if err := os.WriteFile(fixture.Binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
}

func readCursorCreatedSandboxIDs(t *testing.T, fixture *openshell.Fixture) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixture.Directory, "state.json.created-ids"))
	if err != nil {
		t.Fatal(err)
	}
	ids := strings.Fields(string(data))
	return ids
}

func TestCursorIntegrationProbeFailureRedactsBothOutputStreams(t *testing.T) {
	fixture := installProtocolOpenShell(t)
	fixture.EnableCommandExecution()
	project := t.TempDir()
	provider := model.Provider{ID: "cursor", Kind: "acp", ACPProfile: "cursor"}
	command := filepath.Join(project, "agent-fixture")
	writeNativeFixture(t, command, "#!/bin/sh\nif [ \"$(cat \"$HOME/.cursor/cli-config.json\")\" != 'synthetic test credential' ]; then exit 13; fi\nprintf '%s' 'stdout-private-token'\nprintf '%s' 'stderr-private-token' >&2\nexit 9\n")
	if err := os.Chmod(command, 0700); err != nil {
		t.Fatal(err)
	}
	provider.Command = []string{command, "acp"}
	profile, registryDir := cursorIntegrationTestProfile(t, project, provider)
	s := &providerSession{ctx: context.Background()}
	_, err := s.cursorIntegrationTools([]string{command, "acp"}, "ci", profile)
	if err == nil || strings.Contains(err.Error(), "private-token") || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("expected redacted native setup error: %v", err)
	}
	if err := completeStandaloneProfile(profile); err != nil {
		t.Fatal(err)
	}
	assertCursorProbeCleanup(t, registryDir)

	t.Run("unverified cleanup blocks catalog", func(t *testing.T) {
		fixture := installProtocolOpenShell(t)
		fixture.EnableCommandExecution()
		fixture.FailList(t)
		project := t.TempDir()
		provider := model.Provider{ID: "cursor", Kind: "acp", ACPProfile: "cursor"}
		writeNativeFixture(t, filepath.Join(os.Getenv("HOME"), ".cursor/mcp.json"), `{"mcpServers":{"ci":{"command":"node","args":["ci.mjs"]}}}`)
		command := filepath.Join(project, "agent-fixture")
		writeNativeFixture(t, command, "#!/bin/sh\nexit 0\n")
		if err := os.Chmod(command, 0700); err != nil {
			t.Fatal(err)
		}
		provider.Command = []string{command, "acp"}
		profile, registryDir := cursorIntegrationTestProfile(t, project, provider)
		session := &providerSession{ctx: context.Background(), input: Input{ProviderID: "cursor", CWD: project}}
		err := session.prepareCursorIntegrations(provider, profile)
		if !errors.Is(err, execution.ErrIsolationUnverified) {
			t.Fatalf("unverified sandbox cleanup did not block integration setup: %v", err)
		}
		if len(session.nativeServers) != 0 || len(session.output.Integrations) != 0 {
			t.Fatalf("uncertain probe cleanup published integrations: %+v %+v", session.output.Integrations, session.nativeServers)
		}
		remaining, err := execution.ReadStandaloneRuntimes(registryDir)
		if err != nil || len(remaining) != 2 || !cursorRegistryHasState(remaining, profile.ExecutionID, "active") || !cursorRegistryHasOtherState(remaining, profile.ExecutionID, "cleanup_pending") {
			t.Fatalf("uncertain Cursor runtime was not retained for recovery: %+v %v", remaining, err)
		}
	})
}

func cursorRegistryHasState(entries []execution.StandaloneRuntimeLocator, executionID, state string) bool {
	for _, entry := range entries {
		if entry.ExecutionID == executionID && entry.State == state {
			return true
		}
	}
	return false
}

func cursorRegistryHasOtherState(entries []execution.StandaloneRuntimeLocator, executionID, state string) bool {
	for _, entry := range entries {
		if entry.ExecutionID != executionID && entry.State == state {
			return true
		}
	}
	return false
}

func cursorIntegrationTestProfile(t *testing.T, project string, provider model.Provider) (execution.Profile, string) {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("WHIRLWIND_DATA_DIR", dataDir)
	profile, err := execution.ForProvider(project, true, "", provider.Sandbox, provider.Command)
	if err != nil {
		t.Fatal(err)
	}
	if err := ProvisionNativeIntegrations(&profile, provider); err != nil {
		t.Fatal(err)
	}
	if err := provisionACPAuth(&profile, provider); err != nil {
		t.Fatal(err)
	}
	registryDir := filepath.Join(dataDir, "supervisor", "standalone")
	profile, err = pinStandaloneProfile(context.Background(), profile, registryDir)
	if err != nil {
		t.Fatal(err)
	}
	return profile, registryDir
}

func assertCursorProbeCleanup(t *testing.T, registryDir string) {
	t.Helper()
	remaining, err := execution.ReadStandaloneRuntimes(registryDir)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("Cursor probe runtime was not verified and removed: %+v %v", remaining, err)
	}
}
