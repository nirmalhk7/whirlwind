package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

func TestSessionExecutionProfileCarriesBrokerWithoutSerializingToken(t *testing.T) {
	profile, err := execution.ForProvider(t.TempDir(), true, "policy", nil, []string{"codex-acp"})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	descriptor := &execution.MCPBroker{ID: "selected-tools", SocketPath: "/tmp/whirlwind-mcp.sock", Host: "whirlwind-mcp.openshell.internal", Path: "/mcp"}
	in := Input{Execution: &profile, MCPBroker: descriptor, MCPBrokerToken: token}
	encodedInput, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedInput), token) {
		t.Fatal("host-only broker capability leaked into serialized provider input")
	}
	session := &providerSession{input: in, client: &Client{input: in}}
	got, err := session.sessionExecutionProfile(context.Background(), model.Provider{Kind: "acp"}, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.MCPBroker == nil || *got.MCPBroker != *descriptor || got.MCPBrokerToken != token {
		t.Fatalf("broker capability was not attached to the host execution profile: %+v", got.MCPBroker)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatal("host-only broker capability leaked into serialized profile")
	}
	withoutToken := got
	withoutToken.MCPBrokerToken = strings.Repeat("b", 64)
	if withoutToken.Digest() != got.Digest() {
		t.Fatal("host-only broker capability changed execution-profile identity")
	}
}

func TestSelectedIntegrationBrokerConflictsWithCheckOrNativeMountCapabilities(t *testing.T) {
	broker := &execution.MCPBroker{ID: "selected-read-tools", SocketPath: "/tmp/private/broker.sock", Host: "whirlwind-mcp.openshell.internal", Path: "/mcp"}
	for _, test := range []struct {
		name  string
		input Input
		want  string
	}{
		{name: "review checks", input: Input{Role: "review", ReadOnly: true, MCPBroker: broker}, want: "conflicts with reviewer check broker"},
		{name: "raw native mounts", input: Input{NativeIntegrations: true, MCPBroker: broker}, want: "conflicts with raw native integration mounts"},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := &providerSession{input: test.input, hooks: Hooks{RunCheck: func(context.Context, string) (CheckResult, error) { return CheckResult{}, nil }}}
			if _, err := session.prepareProviderIntegrations(model.Provider{Kind: "acp"}, model.Provider{}, &execution.Profile{}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("incompatible MCP capabilities did not stop explicitly: %v", err)
			}
		})
	}
}

func TestACPSessionDeclaresOnlyBrokerIdentityForGuestShim(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "launch.mjs")
	if err := os.WriteFile(filepath.Join(root, "mcp-stdio.mjs"), []byte("export {};"), 0600); err != nil {
		t.Fatal(err)
	}
	profile := execution.Profile{Launcher: launcher}
	input := Input{Execution: &profile, MCPBroker: &execution.MCPBroker{ID: "selected-read-tools", SocketPath: "/tmp/private/broker.sock", Host: "whirlwind-mcp.openshell.internal", Path: "/mcp"}, MCPBrokerToken: strings.Repeat("s", 64)}
	session := &providerSession{input: input}
	servers := session.mcpServers()
	if len(servers) != 1 {
		t.Fatalf("ACP session did not declare selected MCP broker shim: %+v", servers)
	}
	server := servers[0].(map[string]any)
	if server["name"] != "whirlwind_selected_read_tools" || server["command"] != "node" {
		t.Fatalf("ACP selected broker shim has unexpected launch metadata: %+v", server)
	}
	args := server["args"].([]string)
	if len(args) != 2 || args[1] != "selected-read-tools" || strings.Contains(strings.Join(args, " "), input.MCPBrokerToken) {
		t.Fatalf("ACP shim args leaked credentials or lost broker ID: %v", args)
	}
	if len(server["env"].([]any)) != 0 {
		t.Fatalf("ACP shim received environment credentials: %+v", server["env"])
	}
}
