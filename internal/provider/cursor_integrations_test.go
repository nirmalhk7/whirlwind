package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
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
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	writeNativeFixture(t, filepath.Join(home, ".cursor/mcp.json"), `{"mcpServers":{"ci":{"command":"node","args":["ci.mjs"]}}}`)
	command := filepath.Join(project, "agent-fixture")
	writeNativeFixture(t, command, "#!/bin/sh\nif [ \"$1\" != mcp ] || [ \"$2\" != list-tools ] || [ \"$3\" != ci ]; then exit 12; fi\nprintf '%s' '{\"tools\":[{\"name\":\"read_ci\",\"annotations\":{\"readOnlyHint\":true}}]}'\n")
	if err := os.Chmod(command, 0700); err != nil {
		t.Fatal(err)
	}
	p := model.Provider{ID: "cursor", Kind: "acp", Command: []string{command, "acp"}}
	profile, err := execution.ForProvider(project, true, "", nil, p.Command)
	if err != nil {
		t.Fatal(err)
	}
	s := &providerSession{ctx: context.Background(), input: Input{ProviderID: "cursor", CWD: project, DiscoverConnected: true}}
	if err := s.prepareCursorIntegrations(p, profile); err != nil {
		t.Fatal(err)
	}
	if len(s.output.Integrations) != 1 || !s.output.Integrations[0].Callable || len(s.output.Integrations[0].Tools) != 1 || !s.output.Integrations[0].Tools[0].ReadOnly {
		t.Fatalf("native inventory missing: %+v", s.output.Integrations)
	}
	if len(s.nativeServers) != 1 {
		t.Fatalf("native transport missing: %+v", s.nativeServers)
	}
}

func TestCursorIntegrationProbeFailureRedactsBothOutputStreams(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	command := filepath.Join(project, "agent-fixture")
	writeNativeFixture(t, command, "#!/bin/sh\nprintf '%s' 'stdout-private-token'\nprintf '%s' 'stderr-private-token' >&2\nexit 9\n")
	if err := os.Chmod(command, 0700); err != nil {
		t.Fatal(err)
	}
	profile, err := execution.ForProvider(project, true, "", nil, []string{command, "acp"})
	if err != nil {
		t.Fatal(err)
	}
	s := &providerSession{ctx: context.Background()}
	_, err = s.cursorIntegrationTools([]string{command, "acp"}, "ci", profile)
	if err == nil || strings.Contains(err.Error(), "private-token") || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("expected redacted native setup error: %v", err)
	}
}
