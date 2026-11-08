package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

const (
	nativeFixtureCursorConfig  = ".cursor/mcp.json"
	nativeFixtureProjectConfig = ".mcp.json"
)

func TestProvisionNativeIntegrationsKeepsBoundedProviderState(t *testing.T) {
	home, project := nativeProvisionFixture(t)
	for _, test := range []struct {
		provider model.Provider
		want     []string
	}{
		{model.Provider{ID: "codex", Kind: "codex"}, []string{"CODEX_HOME/config.toml", "CODEX_HOME/auth.json", "CODEX_HOME/.credentials.json", "CODEX_HOME/plugins/cache/team/ci/1.0/.codex-plugin/plugin.json", "CODEX_HOME/plugins/cache/team/ci/1.0/.mcp.json"}},
		{model.Provider{ID: "claude", Kind: "claude"}, []string{"HOME/.claude.json", "CLAUDE_CONFIG_DIR/settings.json", "CLAUDE_CONFIG_DIR/.credentials.json"}},
		{model.Provider{ID: "cursor", Kind: "acp"}, []string{"HOME/.cursor/mcp.json", "HOME/.cursor/cli-config.json"}},
	} {
		t.Run(test.provider.ID, func(t *testing.T) {
			profile := execution.Profile{CWD: project, RuntimeDir: "/isolated/runtime", Domains: []string{"existing.internal"}}
			if err := ProvisionNativeIntegrations(&profile, test.provider); err != nil {
				t.Fatal(err)
			}
			assertNativeProvision(t, profile, test.want, home)
			if test.provider.ID == "claude" && !slices.Contains(profile.ReadRoots, filepath.Join(project, nativeFixtureProjectConfig)) {
				t.Fatal("project MCP configuration omitted")
			}
		})
	}
}

func TestNativeMCPDefinitionsResolveBundledACPProfileForCustomProviderID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	writeNativeFixture(t, filepath.Join(project, nativeFixtureProjectConfig), `{"mcpServers":{"ci":{"url":"https://ci.example/mcp"}}}`)
	definitions, err := NativeMCPDefinitions(model.Provider{ID: "team-agent", Kind: "acp", ACPProfile: "claude"}, project)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("custom bundled ACP profile did not resolve its configured MCP inventory: definitions=%v err=%v", definitions, err)
	}
}

func TestNativeMCPDefinitionsProjectPrecedenceAndDisabledServers(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	writeNativeFixture(t, filepath.Join(home, nativeFixtureCursorConfig), `{"mcpServers":{"ci":{"url":"https://old.internal/mcp"},"disabled":{"url":"https://disabled.internal","disabled":true}}}`)
	writeNativeFixture(t, filepath.Join(project, nativeFixtureCursorConfig), `{"mcpServers":{"ci":{"url":"https://new.internal/mcp","headers":{"Authorization":"secret-token"}},"docs":{"command":"node","args":["server.mjs"]}}}`)
	definitions, err := NativeMCPDefinitions(model.Provider{ID: "cursor", Kind: "acp"}, project)
	if err != nil {
		t.Fatal(err)
	}
	var ci struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(definitions["ci"], &ci); err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 2 || ci.URL != "https://new.internal/mcp" || ci.Headers["Authorization"] != "secret-token" {
		t.Fatal("native definitions lost scope precedence or opaque credentials")
	}
	writeNativeFixture(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"ci":{"url":"https://user.internal"},"disabled":{"url":"https://disabled.internal"}}}`)
	writeNativeFixture(t, filepath.Join(project, nativeFixtureProjectConfig), `{"mcpServers":{"ci":{"url":"https://project.internal"},"docs":{"url":"https://docs.internal"}}}`)
	writeNativeFixture(t, filepath.Join(project, ".claude/settings.local.json"), `{"disabledMcpjsonServers":["disabled","docs"]}`)
	definitions, err = NativeMCPDefinitions(model.Provider{ID: "claude", Kind: "claude"}, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 1 || !strings.Contains(string(definitions["ci"]), "project.internal") {
		t.Fatalf("Claude overrides or disable policy ignored: %d definitions", len(definitions))
	}
}

func TestNativeIntegrationProvisionRejectsSymlinksAndUnknownProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	outside := filepath.Join(t.TempDir(), "config.toml")
	writeNativeFixture(t, outside, "sensitive")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".codex/config.toml")); err != nil {
		t.Fatal(err)
	}
	profile := execution.Profile{CWD: t.TempDir()}
	if err := ProvisionNativeIntegrations(&profile, model.Provider{Kind: "codex"}); err == nil {
		t.Fatal("symlink configuration received read/copy authority")
	}
	if err := ProvisionNativeIntegrations(&profile, model.Provider{Kind: "unsupported"}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	writeNativeFixture(t, filepath.Join(home, nativeFixtureCursorConfig), `{"mcpServers":{"private":`)
	if _, err := NativeMCPDefinitions(model.Provider{ID: "cursor", Kind: "acp"}, profile.CWD); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("malformed native definitions accepted or content leaked into error")
	}
}

func TestCursorNativeMCPInterpolationUsesOnlyExplicitApprovedEnvironment(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CI_MCP_TOKEN", "private-token-contains-${unexpanded}")
	t.Setenv("CI_MCP_URL", "https://ci.internal")
	path := filepath.Join(home, nativeFixtureCursorConfig)
	raw := `{"mcpServers":{"ci":{"url":"${env:CI_MCP_URL}/mcp","headers":{"Authorization":"Bearer ${env:CI_MCP_TOKEN}"},"env":{"CI_TOKEN":"${env:CI_MCP_TOKEN}"}}}}`
	writeNativeFixture(t, path, raw)
	definitions, err := NativeMCPDefinitions(model.Provider{ID: "cursor", Kind: "acp", Sandbox: &model.SandboxPolicy{Environment: []string{"CI_MCP_URL", "CI_MCP_TOKEN"}}}, project)
	if err != nil {
		t.Fatal(err)
	}
	var server struct {
		URL          string
		Headers, Env map[string]string
	}
	if err = json.Unmarshal(definitions["ci"], &server); err != nil {
		t.Fatal(err)
	}
	if server.URL != "https://ci.internal/mcp" || server.Headers["Authorization"] != "Bearer private-token-contains-${unexpanded}" || server.Env["CI_TOKEN"] != "private-token-contains-${unexpanded}" {
		t.Fatal("approved inherited environment did not reach native transport unchanged")
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != raw {
		t.Fatal("native source configuration was rewritten")
	}
}

func TestCursorNativeMCPInterpolationFailsClosedWithSanitizedSetupError(t *testing.T) {
	for _, test := range []struct {
		name, value string
		approved    []string
	}{
		{"unapproved", "${env:CI_MCP_PRIVATE}", nil},
		{"unset", "${env:CI_MCP_MISSING}", []string{"CI_MCP_MISSING"}},
		{"loader", "${env:NODE_OPTIONS}", []string{"NODE_OPTIONS"}},
		{"unsupported", "${input:private-prompt}", nil},
		{"workspace", "${workspaceFolder}/private-server", nil},
		{"malformed", "${env:CI_MCP_PRIVATE", []string{"CI_MCP_PRIVATE"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, project := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CI_MCP_PRIVATE", "never-expose-private-secret")
			writeNativeFixture(t, filepath.Join(home, nativeFixtureCursorConfig), `{"mcpServers":{"private-name":{"url":"https://ci.internal","headers":{"Authorization":"Bearer `+test.value+`"}}}}`)
			_, err := NativeMCPDefinitions(model.Provider{ID: "cursor", Kind: "acp", Sandbox: &model.SandboxPolicy{Environment: test.approved}}, project)
			assertNativeMCPSetupError(t, err)
		})
	}
}

func TestCursorNativeMCPInterpolationHonorsEffectiveScopeAndExecutionAllowlist(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CI_MCP_TOKEN", "approved-token")
	writeNativeFixture(t, filepath.Join(home, nativeFixtureCursorConfig), `{"mcpServers":{"ci":{"url":"https://ci.internal","headers":{"Authorization":"${env:CI_MCP_TOKEN}"}},"old":{"url":"${input:old}"}}}`)
	writeNativeFixture(t, filepath.Join(project, nativeFixtureCursorConfig), `{"mcpServers":{"old":{"disabled":true}}}`)
	definitions, err := NativeMCPDefinitions(model.Provider{ID: "cursor", Kind: "acp"}, project, []string{"CI_MCP_TOKEN"})
	if err != nil || len(definitions) != 1 || !strings.Contains(string(definitions["ci"]), "approved-token") {
		t.Fatal("effective execution allowlist or disabled scope ignored")
	}
}

func writeNativeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func nativeProvisionFixture(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		".codex/config.toml": "[mcp_servers.ci]\nurl = \"https://ci.internal/mcp\"\n",
		".codex/auth.json":   "{}", ".codex/.credentials.json": "{}",
		".codex/plugins/cache/team/ci/1.0/.codex-plugin/plugin.json": "{}",
		".codex/plugins/cache/team/ci/1.0/.mcp.json":                 "{}",
		".codex/sessions/private.jsonl":                              "session history",
		".claude.json":                                               "{}", ".claude/settings.json": "{}", ".claude/.credentials.json": "{}",
		nativeFixtureCursorConfig: "{}", ".cursor/cli-config.json": "{}",
	}
	for path, content := range files {
		writeNativeFixture(t, filepath.Join(home, path), content)
	}
	writeNativeFixture(t, filepath.Join(project, nativeFixtureProjectConfig), "{}")
	return home, project
}

func assertNativeProvision(t *testing.T, profile execution.Profile, want []string, home string) {
	t.Helper()
	var got []string
	for _, file := range profile.NativeFiles {
		got = append(got, file.Home+"/"+filepath.ToSlash(file.Path))
		if !slices.Contains(profile.ReadRoots, file.Source) {
			t.Fatalf("source missing bounded read grant: %s", file.Source)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) || profile.RuntimeDir != "/isolated/runtime" || !reflect.DeepEqual(profile.Domains, []string{"existing.internal"}) {
		t.Fatalf("wrong provisioning or expanded authority: %+v", profile)
	}
	if slices.Contains(profile.ReadRoots, home) || slices.Contains(profile.ReadRoots, filepath.Join(home, ".codex")) || strings.Contains(strings.Join(got, "\n"), "sessions") {
		t.Fatalf("broad state access: %+v", profile)
	}
}

func assertNativeMCPSetupError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("missing sanitized actionable setup error: <nil>")
	}
	if !strings.Contains(err.Error(), "MCP") || !strings.Contains(err.Error(), "refresh") {
		t.Fatalf("missing sanitized actionable setup error: %v", err)
	}
	for _, private := range []string{"private", "secret"} {
		if strings.Contains(err.Error(), private) {
			t.Fatalf("missing sanitized actionable setup error: %v", err)
		}
	}
}
