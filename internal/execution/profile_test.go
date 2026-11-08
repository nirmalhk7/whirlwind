package execution

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/process"
	"whirlwind/internal/store"
)

// Dropping role or policy from the digest would let an old session resume with different authority.
func TestProfileDigestChangesWithAuthority(t *testing.T) {
	root := t.TempDir()
	p, err := ForPolicy(root, false, "policy-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := ForPolicy(root, true, "policy-a", nil)
	if p.Digest() == other.Digest() {
		t.Fatal("read-only and coding profiles matched")
	}
	other, _ = ForPolicy(root, false, "policy-b", nil)
	if p.Digest() == other.Digest() {
		t.Fatal("policy change retained session authority")
	}
	brokerA := p
	brokerA.MCPBroker = &MCPBroker{ID: "tools-a", SocketPath: filepath.Join(root, "broker.sock"), Host: "whirlwind-mcp.openshell.internal", Path: "/mcp"}
	brokerA.MCPBrokerToken = strings.Repeat("a", 64)
	brokerB := brokerA
	brokerB.MCPBrokerToken = strings.Repeat("b", 64)
	if brokerA.Digest() != brokerB.Digest() {
		t.Fatal("ephemeral broker token changed profile authority digest")
	}
	brokerB.MCPBroker = &MCPBroker{ID: "tools-a", SocketPath: filepath.Join(root, "broker.sock"), Host: "whirlwind-mcp.openshell.internal", Path: "/mcp"}
	brokerB.MCPBroker.ID = "tools-b"
	if brokerA.Digest() == brokerB.Digest() {
		t.Fatal("selected broker identity did not change profile authority digest")
	}
}

func TestMCPBrokerMustRemainOutsideSandboxMounts(t *testing.T) {
	profile, err := ForPolicy(t.TempDir(), true, "policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile.MCPBroker = &MCPBroker{ID: "selected-tools", SocketPath: filepath.Join(profile.CWD, "broker.sock"), Host: "whirlwind-mcp.openshell.internal", Path: "/mcp"}
	profile.MCPBrokerToken = strings.Repeat("c", 64)
	if err := validateMCPBroker(profile); err == nil || !strings.Contains(err.Error(), "outside sandbox mounts") {
		t.Fatalf("MCP broker socket inside the worktree was accepted: %v", err)
	}
}

func TestEffectiveCheckProfileIncludesResolvedExecutable(t *testing.T) {
	p, _ := ForPolicy(t.TempDir(), true, "policy", nil)
	effective, err := ForCommand(p, []string{"sh", "-c", "true"})
	if err != nil {
		t.Fatal(err)
	}
	if effective.CommandHash == "" || effective.Digest() == p.Digest() {
		t.Fatal("check executable omitted from authority identity")
	}
}

func TestCheckOutputGrantPreparesOnlyDeclaredMissingDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("preserve me"), 0600); err != nil {
		t.Fatal(err)
	}
	profile, err := ForPolicy(root, true, "policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := ForCheck(profile, []string{"dist/cache"})
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(profile.CWD, "dist", "cache")
	info, err := os.Stat(output)
	if err != nil || !info.IsDir() {
		t.Fatalf("declared output directory was not prepared for a bind mount: %v", err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("prepared output directory permits group/other access: %v", info.Mode().Perm())
	}
	if !prepared.ReadOnly || len(prepared.WriteRoots) != 1 || prepared.WriteRoots[0] != output {
		t.Fatalf("check profile broadened write authority: read_only=%t write_roots=%v", prepared.ReadOnly, prepared.WriteRoots)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "preserve me" {
		t.Fatalf("preparing output modified existing source: %q, %v", data, err)
	}
}

// A build-output declaration must never turn existing source files into writable artifacts.
func TestCheckOutputRejectsSourceAndSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	p, _ := ForPolicy(root, true, "p", nil)
	if _, err := ForCheck(p, []string{"src"}); err == nil {
		t.Fatal("source declared writable")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if _, err := ForCheck(p, []string{"out"}); err == nil {
		t.Fatal("output symlink escaped")
	}
}

// Treating policy paths as globs or resolving relative roots could accidentally grant unrelated files.
func TestProfileRejectsUnboundedAndRelativeReadRoots(t *testing.T) {
	for _, path := range []string{"/", "relative", "/tmp/*"} {
		if _, err := ForPolicy(t.TempDir(), true, "p", &model.SandboxPolicy{ReadRoots: []string{path}}); err == nil {
			t.Fatalf("unsafe read root accepted: %s", path)
		}
	}
}

// Leaking ambient secrets or loader overrides gives the child authority absent from committed policy.
func TestCommandFiltersAmbientEnvironment(t *testing.T) {
	t.Setenv("WHIRLWIND_TEST_SECRET", "must-not-leak")
	t.Setenv("NODE_OPTIONS", "--require=/untrusted/inject.js")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "openshell"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	p, _ := ForPolicy(t.TempDir(), true, "p", nil)
	cmd, err := Command(context.Background(), []string{"/bin/echo", "literal $(touch escaped)"}, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "WHIRLWIND_TEST_SECRET=") || strings.HasPrefix(entry, "NODE_OPTIONS=") {
			t.Fatalf("ambient authority leaked: %s", entry)
		}
	}
	if filepath.Base(cmd.Args[0]) == "echo" {
		t.Fatal("command bypassed sandbox launcher")
	}
}

func TestOpenShellBridgeReceivesOnlyHostBootstrapEnvironment(t *testing.T) {
	environment := []string{"PATH=/bin", "WHIRLWIND_OPENSHELL_CLI=/tmp/openshell", "XDG_CONFIG_HOME=/tmp/xdg-config", "XDG_STATE_HOME=/tmp/xdg-state", "OPENSHELL_GATEWAY=local", "OPENSHELL_GATEWAY_ENDPOINT=http://127.0.0.1:8080", "OPENSHELL_WORKSPACE=workspace", "WHIRLWIND_MCP_BROKER_TOKEN=ambient-token-must-not-leak", "WHIRLWIND_ADMIN_TOKEN=must-not-leak", "OPENSHELL_API_KEY=must-not-leak"}
	profile := Profile{Launcher: "/app/bridges/openshell/launch.mjs", MCPBroker: &MCPBroker{ID: "selected-tools", SocketPath: "/tmp/whirlwind-mcp.sock", Host: "whirlwind-mcp.openshell.internal", Path: "/mcp"}, Environment: []string{"OPENSHELL_GATEWAY", "OPENSHELL_API_KEY"}}
	filtered := filteredEnvironment(environment, profile)
	for _, required := range []string{"WHIRLWIND_OPENSHELL_CLI=/tmp/openshell", "XDG_CONFIG_HOME=/tmp/xdg-config", "XDG_STATE_HOME=/tmp/xdg-state", "OPENSHELL_GATEWAY=local", "OPENSHELL_GATEWAY_ENDPOINT=http://127.0.0.1:8080", "OPENSHELL_WORKSPACE=workspace"} {
		if !slices.Contains(filtered, required) {
			t.Fatalf("trusted bridge bootstrap configuration %q was dropped: %v", required, filtered)
		}
	}
	for _, forbidden := range []string{"WHIRLWIND_MCP_BROKER_TOKEN=ambient-token-must-not-leak", "WHIRLWIND_ADMIN_TOKEN=must-not-leak", "OPENSHELL_API_KEY=must-not-leak"} {
		if slices.Contains(filtered, forbidden) {
			t.Fatalf("control-plane credential crossed into bridge/workload environment: %s", forbidden)
		}
	}
}

func TestGeneratedGuestEnvironmentCannotBeOverriddenByPolicy(t *testing.T) {
	reserved := []string{"HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "TMPDIR", "TMP", "TEMP", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"}
	policy := &model.SandboxPolicy{Environment: reserved}
	for _, name := range reserved {
		if EnvironmentOverrideAllowed(name, policy) {
			t.Errorf("policy can override generated guest environment %s", name)
		}
	}
	if _, err := ForPolicy(t.TempDir(), false, "policy", policy); err == nil {
		t.Fatal("policy accepted environment names reserved for generated guest paths")
	}
	if !EnvironmentOverrideAllowed("CUSTOM_API_KEY", &model.SandboxPolicy{Environment: []string{"CUSTOM_API_KEY"}}) {
		t.Fatal("authorized provider environment was rejected")
	}
	if !EnvironmentOverrideAllowed("LANG", nil) {
		t.Fatal("standard locale environment was rejected")
	}
}

// A missing sandbox must fail before an otherwise valid target process can start.
func TestMissingSandboxNeverRunsTarget(t *testing.T) {
	p, _ := ForPolicy(t.TempDir(), false, "p", nil)
	p.Launcher = filepath.Join(t.TempDir(), "missing.mjs")
	target := filepath.Join(t.TempDir(), "escaped")
	cmd, err := Command(context.Background(), []string{"/usr/bin/touch", target}, p)
	if err == nil {
		err = cmd.Run()
	}
	if err == nil {
		t.Fatal("missing sandbox accepted")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("target ran without sandbox")
	}
}

func TestMissingOpenShellNeverRunsTarget(t *testing.T) {
	profile, err := ForPolicy(t.TempDir(), false, "policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js unavailable")
	}
	bin := t.TempDir()
	if err := os.Symlink(node, filepath.Join(bin, "node")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	target := filepath.Join(t.TempDir(), "escaped")
	cmd, err := Command(context.Background(), []string{"/usr/bin/touch", target}, profile)
	if err == nil {
		err = cmd.Run()
	}
	if err == nil || !strings.Contains(err.Error(), "NVIDIA OpenShell CLI unavailable") {
		t.Fatalf("missing OpenShell did not report actionable failure: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("target ran without OpenShell")
	}
}

func TestOpenShellRuntimeHonorsValidatedExplicitCLIPath(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "openshell")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", bin)
	profile, err := ForPolicy(t.TempDir(), true, "policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sandboxRuntime(profile); err != nil {
		t.Fatalf("valid explicit OpenShell CLI was ignored: %v", err)
	}
}

func TestOpenShellProxyTokenIsRegisteredForTraceRedaction(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "openshell"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	profile, err := ForPolicy(t.TempDir(), true, "policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	brokerToken := strings.Repeat("b", 64)
	profile.MCPBroker = &MCPBroker{ID: "selected-tools", SocketPath: filepath.Join(t.TempDir(), "broker.sock"), Host: "whirlwind-mcp.openshell.internal", Path: "/mcp"}
	profile.MCPBrokerToken = brokerToken
	cmd, err := Command(context.Background(), []string{"sh", "-c", "true"}, profile)
	if err != nil {
		t.Fatal(err)
	}
	var token string
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "WHIRLWIND_OPENSHELL_PROXY_TOKEN=") {
			token = strings.TrimPrefix(entry, "WHIRLWIND_OPENSHELL_PROXY_TOKEN=")
		}
	}
	if len(token) < 64 {
		t.Fatalf("sandbox proxy token missing or too short: %q", token)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), token) {
		t.Fatal("proxy token leaked into process arguments")
	}
	var serializedProfile string
	for index, arg := range cmd.Args {
		if arg == "--" && index > 0 {
			serializedProfile = cmd.Args[index-1]
			break
		}
	}
	if strings.Contains(serializedProfile, brokerToken) {
		t.Fatal("MCP broker capability leaked into serialized profile")
	}
	if !slices.Contains(cmd.Env, "WHIRLWIND_MCP_BROKER_TOKEN="+brokerToken) {
		t.Fatal("MCP broker capability was not passed to trusted host bridge")
	}
	if got := store.RedactText("trace=" + brokerToken); got != "trace=[redacted]" {
		t.Fatalf("MCP broker capability was not registered for trace redaction: %q", got)
	}
	if got := store.RedactText("trace=" + token); got != "trace=[redacted]" {
		t.Fatalf("proxy token was not registered for trace redaction: %q", got)
	}
	basic := base64.StdEncoding.EncodeToString([]byte("whirlwind:" + token))
	if got := store.RedactText("Proxy-Authorization: Basic " + basic); strings.Contains(got, basic) {
		t.Fatalf("proxy Basic credential was not registered for trace redaction: %q", got)
	}
	if err := process.Forget(cmd); err != nil {
		t.Fatal(err)
	}
	if got := store.RedactText(token); got != token {
		t.Fatalf("proxy redaction registration outlived process cleanup: %q", got)
	}
	if got := store.RedactText(brokerToken); got != brokerToken {
		t.Fatalf("MCP broker redaction registration outlived process cleanup: %q", got)
	}
}

func TestExecutionProfileSelectsOpenShellBoundary(t *testing.T) {
	profile, err := ForPolicy(t.TempDir(), false, "policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("bridges", "openshell", "launch.mjs")
	if !strings.HasSuffix(profile.Launcher, want) {
		t.Fatalf("execution selected %q, want NVIDIA OpenShell launcher ending in %q", profile.Launcher, want)
	}
}

func TestOpenShellReceivesConfiguredCommandForLinuxImage(t *testing.T) {
	profile, err := ForPolicy(t.TempDir(), false, "policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	openShell := filepath.Join(bin, "openshell")
	if err := os.WriteFile(openShell, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, args, err := prepareSandboxCommand([]string{"image-only-acp", "--stdio"}, profile)
	if err != nil {
		t.Fatal(err)
	}
	if got := args[3]; got != "image-only-acp" {
		t.Fatalf("OpenShell command = %q, want configured image command %q", got, "image-only-acp")
	}
	var sandboxProfile Profile
	if err := json.Unmarshal([]byte(args[1]), &sandboxProfile); err != nil {
		t.Fatal(err)
	}
	for _, root := range sandboxProfile.ReadRoots {
		if strings.Contains(root, "image-only-acp") {
			t.Fatalf("host executable path was mounted into the Linux image: %q", root)
		}
	}
}

func TestOpenShellProviderProfileDoesNotMountHostProviderRuntime(t *testing.T) {
	profile, err := ForProvider(t.TempDir(), false, "policy", nil, []string{"sh", "--stdio"})
	if err != nil {
		t.Fatal(err)
	}
	hostCommand, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	hostCommand, err = filepath.EvalSymlinks(hostCommand)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range profile.ReadRoots {
		if root == hostCommand {
			t.Fatalf("host provider runtime leaked into image-scoped read grants: %q", root)
		}
	}
}
