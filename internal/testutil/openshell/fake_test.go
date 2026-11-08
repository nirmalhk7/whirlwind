package openshell

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPinnedGatewaySchema(t *testing.T) {
	fixture := Install(t)
	var gateways []map[string]any
	decodeCLI(t, fixture, &gateways, "gateway", "list", "--output", "json")
	if len(gateways) != 1 || gateways[0]["active"] != true || gateways[0]["is_remote"] != false || gateways[0]["endpoint"] != "http://127.0.0.1:8080" {
		t.Fatalf("gateway list is not a healthy local v0.1.7-shaped response: %#v", gateways)
	}
	var status map[string]any
	decodeCLI(t, fixture, &status, "status", "--output", "json")
	if status["status"] != "connected" || status["authentication"].(map[string]any)["status"] != "not_required" {
		t.Fatalf("gateway status schema mismatch: %#v", status)
	}
	var info map[string]any
	decodeCLI(t, fixture, &info, "gateway", "info", "--output", "json")
	drivers := info["compute_drivers"].([]any)
	if drivers[0].(map[string]any)["name"] != "docker" {
		t.Fatalf("gateway driver schema mismatch: %#v", info)
	}
}

func TestPinnedGatewayFixtureExposesAuthenticationAndInfoHealthIndependently(t *testing.T) {
	fixture := Install(t)
	fixture.SetGatewayHealth(t, "connected", "unverified", "unhealthy")
	var status map[string]any
	decodeCLI(t, fixture, &status, "status", "--output", "json")
	if status["status"] != "connected" || status["authentication"].(map[string]any)["status"] != "unverified" {
		t.Fatalf("gateway health/authentication fields collapsed: %#v", status)
	}
	var info map[string]any
	decodeCLI(t, fixture, &info, "gateway", "info", "--output", "json")
	if info["status"] != "unhealthy" {
		t.Fatalf("gateway info health field was not independently controlled: %#v", info)
	}
}

func TestSandboxLifecycleSchemaAndCallLog(t *testing.T) {
	fixture := Install(t)
	var created map[string]any
	create := []string{"sandbox", "create", "--name", "attempt-test", "--from", "linux-test-image", "--policy", "/tmp/policy.json", "--driver-config-json", `{}`, "--label", "whirlwind.io/execution=attempt-test", "--detach", "--no-keep", "--no-auto-providers"}
	create = append(create, pinnedAuthority()...)
	create = append(create, "--", "/bin/sh", "-c", "exec sleep infinity")
	decodeCLI(t, fixture, &created, create...)
	if created["id"] != "test-sandbox-attempt-test" || created["name"] != "attempt-test" || created["workspace"] != "whirlwind-test-workspace" || created["phase"] != "Ready" || created["labels"].(map[string]any)["whirlwind.io/execution"] != "attempt-test" {
		t.Fatalf("sandbox create schema mismatch: %#v", created)
	}
	if !fixture.HasInertInit(t) {
		t.Fatal("fake CLI did not verify the detached inert init command")
	}
	var stopped map[string]any
	decodeCLI(t, fixture, &stopped, append([]string{"sandbox", "stop", "attempt-test"}, pinnedAuthority()...)...)
	if stopped["id"] != created["id"] || stopped["phase"] != "Stopped" {
		t.Fatalf("sandbox stop did not preserve identity and report stopped phase: %#v", stopped)
	}
	var list struct {
		Sandboxes []map[string]any `json:"sandboxes"`
		NextPage  string           `json:"next_page_token"`
	}
	decodeCLI(t, fixture, &list, append([]string{"sandbox", "list", "--output", "json"}, pinnedAuthority()...)...)
	if len(list.Sandboxes) != 1 || list.Sandboxes[0]["id"] != created["id"] || list.NextPage != "" {
		t.Fatalf("sandbox list omitted lifecycle identity: %#v", list)
	}
	calls := fixture.Calls(t)
	if len(calls) != 3 || calls[0][0] != "sandbox" || calls[0][1] != "create" || calls[1][1] != "stop" || calls[2][1] != "list" {
		t.Fatalf("lifecycle arguments were not captured in order: %#v", calls)
	}
}

func TestSandboxCreateRejectsNonInertDetachedInit(t *testing.T) {
	fixture := Install(t)
	args := []string{"sandbox", "create", "--name", "attempt-test", "--from", "linux-test-image", "--policy", "/tmp/policy.json", "--driver-config-json", `{}`, "--detach", "--no-keep", "--no-auto-providers"}
	args = append(args, pinnedAuthority()...)
	args = append(args, "--", "/bin/sh", "-c", "echo unsafe")
	if _, err := fixture.RunCLI(t, args...); err == nil {
		t.Fatal("fake CLI accepted a detached init that could perform work before ACP exec")
	}
}

func TestSandboxCreateRejectsOutputJSONWithNoKeep(t *testing.T) {
	fixture := Install(t)
	args := []string{"sandbox", "create", "--name", "attempt-test", "--from", "linux-test-image", "--policy", "/tmp/policy.json", "--driver-config-json", `{}`, "--detach", "--no-keep", "--no-auto-providers", "--output", "json"}
	args = append(args, pinnedAuthority()...)
	args = append(args, "--", "/bin/sh", "-c", "exec sleep infinity")
	if _, err := fixture.RunCLI(t, args...); err == nil {
		t.Fatal("fake CLI accepted the release-incompatible --no-keep and --output json combination")
	}
}

func TestSandboxStopRejectsStructuredOutput(t *testing.T) {
	fixture := Install(t)
	seed := []string{"sandbox", "create", "--name", "attempt-test", "--from", "linux-test-image", "--policy", "/tmp/policy.json", "--driver-config-json", `{}`, "--label", "whirlwind.io/execution=attempt-test", "--detach", "--no-auto-providers"}
	seed = append(seed, pinnedAuthority()...)
	seed = append(seed, "--", "/bin/sh", "-c", "exec sleep infinity")
	if _, err := fixture.RunCLI(t, seed...); err != nil {
		t.Fatal(err)
	}
	stop := append([]string{"sandbox", "stop", "attempt-test", "--output", "json"}, pinnedAuthority()...)
	if _, err := fixture.RunCLI(t, stop...); err == nil {
		t.Fatal("fake CLI accepted unsupported structured output on sandbox stop")
	}
}

func TestSandboxCommandsRequirePinnedGatewayAndWorkspace(t *testing.T) {
	fixture := Install(t)
	if _, err := fixture.RunCLI(t, "sandbox", "list", "--output", "json"); err == nil {
		t.Fatal("sandbox list succeeded without explicit gateway, endpoint, and workspace")
	}
}

func TestFixtureRecordsOnlyCustomXDGPathForLifecycleCalls(t *testing.T) {
	fixture := Install(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "private-config"))
	_, err := fixture.RunCLI(t, append([]string{"gateway", "list"}, pinnedAuthority()...)...)
	if err != nil {
		t.Fatal(err)
	}
	calls := fixture.EnvironmentCalls(t)
	if len(calls) != 1 || !calls[0].XDGPresent || calls[0].XDGConfigHome != os.Getenv("XDG_CONFIG_HOME") {
		t.Fatalf("custom XDG path metadata not captured: %#v", calls)
	}
}

func TestFixtureRequiresDocumentedSandboxListSelectorFlag(t *testing.T) {
	fixture := Install(t)
	wrong := append([]string{"sandbox", "list"}, pinnedAuthority()...)
	wrong = append(wrong, "--output", "json", "--label-selector", "whirlwind.io/execution=test")
	if _, err := fixture.RunCLI(t, wrong...); err == nil {
		t.Fatal("fake OpenShell accepted a selector flag absent from the pinned CLI")
	}
	right := append([]string{"sandbox", "list"}, pinnedAuthority()...)
	right = append(right, "--output", "json", "--selector", "whirlwind.io/execution=test")
	if _, err := fixture.RunCLI(t, right...); err != nil {
		t.Fatalf("fake OpenShell rejected the documented selector flag: %v", err)
	}
}

func TestFixtureExecutesFinalPayloadOnlyWhenExplicitlyEnabled(t *testing.T) {
	fixture := Install(t)
	input := strings.NewReader("export HTTP_PROXY='http://proxy.invalid:8080'\n")
	seed := []string{"sandbox", "exec"}
	seed = append(seed, pinnedAuthority()...)
	seed = append(seed, "--", "/bin/sh", "-c", "umask 077; cat > \"$1\"", "whirlwind-env", "/tmp/ww-attempt.env")
	if _, err := fixture.RunCLIWithInput(t, input, seed...); err != nil {
		t.Fatal(err)
	}
	runPayload := func() ([]byte, error) {
		args := []string{"sandbox", "exec"}
		args = append(args, pinnedAuthority()...)
		args = append(args, "--", "/bin/sh", "-c", `set -a; . "$1" || exit; rm -f "$1"; shift; exec "$@"`, "whirlwind-env", "/tmp/ww-attempt.env", "/usr/bin/printf", "payload-ran")
		return fixture.RunCLI(t, args...)
	}
	if _, err := runPayload(); err == nil {
		t.Fatal("disabled fake OpenShell executed an agent payload")
	}
	fixture.EnableCommandExecution()
	output, err := runPayload()
	if err != nil || string(output) != "payload-ran" {
		t.Fatalf("explicit test payload did not run: output=%q error=%v", output, err)
	}
}

func TestFixtureRecordsOnlyEnvironmentSeedKeyNames(t *testing.T) {
	fixture := Install(t)
	var capability [32]byte
	if _, err := rand.Read(capability[:]); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(capability[:])
	input := "export HTTP_PROXY='http://user:" + token + "@host.openshell.internal:1234'\nexport http_proxy='http://user:" + token + "@host.openshell.internal:1234'\n"
	args := []string{"sandbox", "exec"}
	args = append(args, pinnedAuthority()...)
	args = append(args, "--", "/bin/sh", "-c", "umask 077; cat > /tmp/ww-test.env")
	if _, err := fixture.RunCLIWithInput(t, strings.NewReader(input), args...); err != nil {
		t.Fatal(err)
	}
	if got := fixture.EnvironmentSeedKeys(t); !reflect.DeepEqual(got, []string{"HTTP_PROXY", "http_proxy"}) {
		t.Fatalf("env bootstrap fixture did not retain only key names: %#v", got)
	}
	for _, call := range fixture.Calls(t) {
		if strings.Contains(strings.Join(call, " "), token) {
			t.Fatal("stdin secret appeared in recorded command arguments")
		}
	}
	state, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), token) {
		t.Fatal("stdin secret value appeared in fake CLI state")
	}
}

func TestFixtureRecordsPerExecutionGuestHomeScopeWithoutValues(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		env       string
		want      bool
		forbidden string
	}{
		{
			name:      "per-execution runtime home",
			env:       "export HOME='/tmp/ww-attempt-123/home'\nexport CODEX_HOME='/tmp/ww-attempt-123/home/codex'\nexport CLAUDE_CONFIG_DIR='/tmp/ww-attempt-123/home/claude'\nexport TMPDIR='/tmp/ww-attempt-123/tmp'\n",
			want:      true,
			forbidden: "/tmp/ww-attempt-123/home",
		},
		{
			name:      "host home",
			env:       "export HOME='/Users/person'\nexport CODEX_HOME='/Users/person/.codex'\nexport CLAUDE_CONFIG_DIR='/Users/person/.claude'\nexport TMPDIR='/tmp'\n",
			want:      false,
			forbidden: "/Users/person",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := Install(t)
			args := []string{"sandbox", "exec"}
			args = append(args, pinnedAuthority()...)
			args = append(args, "--", "/bin/sh", "-c", "umask 077; cat > /tmp/ww-attempt.env")
			if _, err := fixture.RunCLIWithInput(t, strings.NewReader(testCase.env), args...); err != nil {
				t.Fatal(err)
			}
			state, err := os.ReadFile(fixture.statePath)
			if err != nil {
				t.Fatal(err)
			}
			var recorded struct {
				HomeScoped bool `json:"runtime_home_scoped"`
			}
			if err := json.Unmarshal(state, &recorded); err != nil {
				t.Fatal(err)
			}
			if recorded.HomeScoped != testCase.want {
				t.Fatalf("guest runtime home scope=%t, want %t", recorded.HomeScoped, testCase.want)
			}
			if strings.Contains(string(state), testCase.forbidden) {
				t.Fatal("fake CLI persisted a workload environment value")
			}
		})
	}
}

func pinnedAuthority() []string {
	return []string{"--gateway", "whirlwind-test-local", "--gateway-endpoint", "http://127.0.0.1:8080", "--workspace", "whirlwind-test-workspace"}
}

func decodeCLI(t *testing.T, fixture *Fixture, target any, args ...string) {
	t.Helper()
	output, err := fixture.RunCLI(t, args...)
	if err != nil {
		t.Fatalf("fake openshell %v: %v", args, err)
	}
	if err := json.Unmarshal(output, target); err != nil {
		t.Fatalf("fake openshell %v returned invalid JSON %q: %v", args, output, err)
	}
}
