package execution

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/process"
)

const liveAcceptanceVariable = "WHIRLWIND_OPENSHELL_LIVE_ACCEPTANCE"

func TestLiveOpenShellAcceptance(t *testing.T) {
	if os.Getenv(liveAcceptanceVariable) != "1" {
		t.Skipf("set %s=1 with a disposable local OpenShell gateway and Linux fake ACP image", liveAcceptanceVariable)
	}
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	if err := os.Mkdir(worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "init", "--quiet", worktree).CombinedOutput(); err != nil {
		t.Fatalf("initialize disposable live-probe worktree: %v: %s", err, output)
	}
	worktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	writable := liveProfile(t, worktree, false, 2*time.Minute)
	runLiveCommand(t, writable, []string{"/bin/sh", "-c", "touch accepted.txt; test -f accepted.txt; printf writable-ok"})
	if _, err := os.Stat(filepath.Join(worktree, "accepted.txt")); err != nil {
		t.Fatalf("OpenShell did not persist allowed worktree write: %v", err)
	}

	readOnly := liveProfile(t, worktree, true, 2*time.Minute)
	if output, err := runLiveCommandResult(t, readOnly, []string{"/bin/sh", "-c", "if touch readonly-denied.txt 2>/dev/null; then echo write-was-allowed; exit 31; else echo readonly-denied; fi"}); err != nil || !strings.Contains(output, "readonly-denied") {
		t.Fatalf("read-only role denial was not observed in a running payload: %v: %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(worktree, "readonly-denied.txt")); !os.IsNotExist(err) {
		t.Fatalf("read-only role created a host file: %v", err)
	}
	outsideFile := fmt.Sprintf("/home/whirlwind/whirlwind-outside-%d", time.Now().UnixNano())
	image := os.Getenv("WHIRLWIND_OPENSHELL_IMAGE")
	baseline := exec.Command("docker", "run", "--rm", "--entrypoint", "/bin/sh", image, "-c", `test "$(id -u)" = 502 && test -w /home/whirlwind && touch "$1" && rm "$1"`, "probe", outsideFile)
	if output, err := baseline.CombinedOutput(); err != nil {
		t.Fatalf("image outside-worktree path is not writable before policy: %v: %s", err, output)
	}
	outsideProfile := liveProfile(t, worktree, false, 2*time.Minute)
	if output, err := runLiveCommandResult(t, outsideProfile, []string{"/bin/sh", "-c", `if touch "$1" 2>/dev/null; then echo outside-write-was-allowed; exit 32; else echo outside-write-denied; fi`, "probe", outsideFile}); err != nil || !strings.Contains(output, "outside-write-denied") {
		t.Fatalf("outside-worktree write denial was not observed in a running payload: %v: %s", err, output)
	}
	if _, err := os.Stat(outsideFile); !os.IsNotExist(err) {
		t.Fatalf("outside path was unexpectedly created on the host: %v", err)
	}

	network := liveProfile(t, worktree, true, 2*time.Minute)
	networkCommand := []string{"/bin/sh", "-c", strings.Join([]string{
		"test -n \"$HTTP_PROXY\" && test -n \"$HTTPS_PROXY\" && test -n \"$http_proxy\" && test -n \"$https_proxy\" && printf proxy-env-present",
		`status=$(curl -sS --max-time 20 -D /tmp/whirlwind-http-headers -o /dev/null -w '%{http_code}' http://example.com/); denial=$(awk 'tolower($1)=="x-whirlwind-proxy-denial:" {print tolower($2); exit}' /tmp/whirlwind-http-headers); case "$denial" in authentication|target|dns_policy|upstream_connection) category="$denial";; *) category=absent_or_unknown;; esac; printf 'http_status=%s denial=%s\n' "$status" "$category"; case "$status" in 2*|3*) :;; *) exit 41;; esac`,
		`https_status=$(curl -sS --max-time 20 -D /tmp/whirlwind-https-headers -o /dev/null -w '%{http_code}' https://example.com/); case "$https_status" in 2*|3*) :;; *) printf 'https_status=%s\n' "$https_status"; exit 42;; esac`,
		`if curl --noproxy '*' --connect-timeout 5 --max-time 8 -sS -o /dev/null http://1.1.1.1/; then printf direct-egress-was-allowed; exit 43; else code=$?; test "$code" -eq 7 && printf direct-egress-denied; fi`,
		"test \"$(curl --noproxy '' -sS --max-time 8 -o /dev/null -w '%{http_code}' --proxy \"$HTTP_PROXY\" http://example.com:22/)\" = 403",
		"test \"$(curl --noproxy '' -sS --max-time 8 -o /dev/null -w '%{http_code}' --proxy \"$HTTP_PROXY\" http://example.com:8080/)\" = 403",
		"test \"$(curl --noproxy '' -sS --max-time 8 -o /dev/null -w '%{http_code}' --proxy \"$HTTP_PROXY\" http://127.0.0.1:80/)\" = 403",
	}, " && ")}
	if output, err := runLiveCommandResult(t, network, networkCommand); err != nil {
		t.Fatalf("web egress/DNS or denied-port policy failed: %v: %s", err, output)
	}

	auth := filepath.Join(root, "synthetic-auth.json")
	if err := os.WriteFile(auth, []byte(`{"synthetic":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	credentialProfile := liveProfile(t, worktree, true, 2*time.Minute)
	credentialProfile.NativeFiles = []NativeFile{{Source: auth, Home: "CODEX_HOME", Path: "auth.json"}}
	homeRoot := "/tmp/ww-" + credentialProfile.ExecutionID + "/home"
	stagingRoot := "/tmp/ww-native-" + credentialProfile.ExecutionID
	credentialCommand := []string{"/bin/sh", "-c", `set -eu
test "$HOME" = "$1"
test "$CODEX_HOME" = "$HOME/codex"
test "$CLAUDE_CONFIG_DIR" = "$HOME/claude"
test "$TMPDIR" = "${HOME%/home}/tmp"
test -r "$CODEX_HOME/auth.json"
grep -qx '{"synthetic":true}' "$CODEX_HOME/auth.json"
test ! -e "$CODEX_HOME/config.toml"
test ! -e "$CLAUDE_CONFIG_DIR/.credentials.json"
test -r "$2/CODEX_HOME/auth.json"
test ! -w "$2/CODEX_HOME/auth.json"
mkdir -p "$HOME/.cache/codex" "$CODEX_HOME/sessions" "$CLAUDE_CONFIG_DIR/sessions" "$TMPDIR/cache"
touch "$HOME/.cache/codex/session" "$CODEX_HOME/sessions/session" "$CLAUDE_CONFIG_DIR/sessions/session" "$TMPDIR/cache/file"
if touch "$3/runtime-home-repo-write" 2>/dev/null; then echo repository-write-was-allowed; exit 51; else echo repository-remains-readonly; fi
test ! -e "$3/runtime-home-repo-write"
printf runtime-home-and-selected-auth-ok`, "probe", homeRoot, stagingRoot, worktree}
	if output, err := runLiveCommandResult(t, credentialProfile, credentialCommand); err != nil || !strings.Contains(output, "runtime-home-and-selected-auth-ok") || !strings.Contains(output, "repository-remains-readonly") {
		t.Fatalf("synthetic selected credential was not isolated from unrelated home files: %v: %s", err, output)
	}

	timeoutProfile := liveProfile(t, worktree, true, 30*time.Second)
	if output, err := runLiveCommandResult(t, timeoutProfile, []string{"/bin/sh", "-c", "printf child-started; sleep 60"}); err == nil || !strings.Contains(output, "child-started") {
		t.Fatalf("long child did not start and then honor its persisted attempt deadline: %v: %s", err, output)
	}
	lifecycle, ok, err := ReadOpenShellLifecycle(timeoutProfile)
	if err != nil || !ok || lifecycle.SandboxPhase != "Stopped" || lifecycle.SandboxHealth != "stopped" {
		t.Fatalf("deadline cleanup did not verify sandbox isolation: lifecycle=%+v exists=%t error=%v", lifecycle, ok, err)
	}
}

func liveProfile(t *testing.T, worktree string, readOnly bool, lifetime time.Duration) Profile {
	t.Helper()
	profile, err := ForPolicy(worktree, readOnly, "live-open-shell-acceptance", &model.SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if !readOnly {
		profile.AllowedPaths = []string{"**"}
	}
	id := fmt.Sprintf("live-%d", time.Now().UnixNano())
	deadline := time.Now().Add(lifetime).UTC().Format(time.RFC3339Nano)
	profile, err = PinOpenShellRuntime(context.Background(), profile, id, 1, deadline, filepath.Join(t.TempDir(), "supervisor"))
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func runLiveCommand(t *testing.T, profile Profile, command []string) {
	t.Helper()
	output, err := runLiveCommandResult(t, profile, command)
	if err != nil {
		t.Fatalf("live OpenShell command failed: %v: %s", err, output)
	}
}

func runLiveCommandResult(t *testing.T, profile Profile, command []string) (string, error) {
	t.Helper()
	cmd, err := Command(context.Background(), command, profile)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = process.Run(cmd)
	return output.String(), err
}
