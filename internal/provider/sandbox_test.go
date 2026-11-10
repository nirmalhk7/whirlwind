package provider

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

const (
	sandboxAllowedPath   = "allowed/**"
	sandboxWriteTextFile = "fs/write_text_file"
	sandboxCloseError    = "close ACP client: %v"
)

// Permission allow decisions must never widen the process filesystem boundary.
func TestACPTerminalCannotWriteOutsideSandbox(t *testing.T) {
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", "")
	outside := filepath.Join(t.TempDir(), "outside-terminal-write")
	if err := os.WriteFile(outside, []byte("host-writable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(outside); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := execution.ForPolicy(root, false, "nono-terminal", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile.AllowedPaths = []string{"**"}
	c := NewClient(Input{CWD: root, Execution: &profile}, Hooks{Permission: func(context.Context, Permission) (bool, error) { return true, nil }})
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := context.Background()
	command := `if touch "$1" 2>/dev/null; then echo outside-write-was-allowed; exit 31; else echo outside-write-denied; fi`
	v, err := c.Handle(ctx, clientMessage("terminal/create", map[string]any{"command": "/bin/sh", "args": []string{"-c", command, "terminal-probe", outside}}))
	if err != nil {
		t.Fatal(err)
	}
	id := v.(map[string]any)["terminalId"]
	v, err = c.Handle(ctx, clientMessage("terminal/wait_for_exit", map[string]any{"terminalId": id}))
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["exitCode"] != 0 {
		t.Fatalf("denial proof wrapper did not complete successfully: %v", v)
	}
	v, err = c.Handle(ctx, clientMessage("terminal/output", map[string]any{"terminalId": id}))
	if err != nil {
		t.Fatal(err)
	}
	output := v.(map[string]any)["output"].(string)
	if !strings.Contains(output, "outside-write-denied") || strings.Contains(output, "outside-write-was-allowed") {
		t.Fatalf("running payload did not prove the image-writable outside path was denied: %q", output)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("nono terminal cleanup: %v", err)
	}
}

// A model must not change the host wrapper's executable lookup, temp directory, home or crypto configuration.
func TestTerminalEnvironmentCannotReconfigureSandboxHost(t *testing.T) {
	for _, name := range []string{"PATH", "HOME", "TMPDIR", "OPENSSL_CONF", "NODE_OPTIONS"} {
		var params clientParams
		raw, _ := json.Marshal(map[string]any{"env": []map[string]string{{"name": name, "value": "/untrusted"}}})
		if err := json.Unmarshal(raw, &params); err != nil {
			t.Fatal(err)
		}
		if err := params.applyEnvironment(exec.Command("echo")); err == nil {
			t.Fatalf("host runtime override accepted: %s", name)
		}
	}
}

// Host-mediated ACP writes must obey the same committed scope as OS-sandboxed subprocesses.
func TestACPFileWriteCannotBypassCommittedScope(t *testing.T) {
	root := t.TempDir()
	c := NewClient(Input{CWD: root, Policy: model.Policy{AllowedPaths: []string{sandboxAllowedPath}}}, Hooks{Permission: func(context.Context, Permission) (bool, error) { return true, nil }})
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf(sandboxCloseError, err)
		}
	})
	if _, err := c.Handle(context.Background(), clientMessage(sandboxWriteTextFile, map[string]string{"path": "blocked.txt", "content": "bad"})); err == nil {
		t.Fatal("host callback escaped committed scope")
	}
	if _, err := os.Stat(filepath.Join(root, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatal("out-of-scope file created")
	}
}

func TestACPFilePermissionCannotRaceFilesystemBoundary(t *testing.T) {
	for _, target := range []string{"outside", ".git", "blocked"} {
		t.Run(target, func(t *testing.T) { assertPermissionSymlinkSwapRejected(t, target) })
	}
}

func assertPermissionSymlinkSwapRejected(t *testing.T, target string) {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	protected := t.TempDir()
	if target != "outside" {
		protected = filepath.Join(root, target)
		if err := os.Mkdir(protected, 0700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(protected, "result.txt")
	if err := os.WriteFile(secret, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(root, "allowed")
	if err := os.Mkdir(allowed, 0700); err != nil {
		t.Fatal(err)
	}
	client := NewClient(Input{CWD: root, Policy: model.Policy{AllowedPaths: []string{sandboxAllowedPath}}}, Hooks{Permission: func(context.Context, Permission) (bool, error) {
		if err := os.Rename(allowed, filepath.Join(root, "previous")); err != nil {
			return false, err
		}
		return true, os.Symlink(protected, allowed)
	}})
	closeClient(t, client)
	_, err := client.Handle(context.Background(), clientMessage(sandboxWriteTextFile, map[string]string{"path": "allowed/result.txt", "content": "escaped"}))
	if err == nil {
		t.Error("symlink swap during permission approval escaped filesystem boundary")
	}
	assertProtectedContentUnchanged(t, secret)
}

func closeClient(t *testing.T, client *Client) {
	t.Helper()
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf(sandboxCloseError, err)
		}
	})
}

func assertProtectedContentUnchanged(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "unchanged" {
		t.Fatalf("protected target changed: %q %v", got, err)
	}
}

func TestACPWriteRejectsProtectedHardlink(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	secret := filepath.Join(root, ".whirlwind.yaml")
	if err := os.WriteFile(secret, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "allowed"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(secret, filepath.Join(root, "allowed", "alias")); err != nil {
		t.Fatal(err)
	}
	c := NewClient(Input{CWD: root, Policy: model.Policy{AllowedPaths: []string{sandboxAllowedPath}}}, Hooks{Permission: func(context.Context, Permission) (bool, error) { return true, nil }})
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close sandbox client: %v", err)
		}
	})
	_, err := c.Handle(context.Background(), clientMessage(sandboxWriteTextFile, map[string]string{"path": "allowed/alias", "content": "escaped"}))
	if err == nil {
		t.Error("protected hardlink accepted")
	}
	got, err := os.ReadFile(secret)
	if err != nil || string(got) != "unchanged" {
		t.Fatalf("protected target changed: %q %v", got, err)
	}
}

func TestACPFileOpenRefusesSwappedSymlinkComponents(t *testing.T) {
	for _, write := range []bool{false, true} {
		for _, ancestor := range []bool{false, true} {
			assertSymlinkOpenRejected(t, write, ancestor)
		}
	}
}

func assertSymlinkOpenRejected(t *testing.T, write, ancestor bool) {
	t.Helper()
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	relative, target := "alias", filepath.Join(outside, "secret")
	if ancestor {
		target, relative = outside, "alias/secret"
	}
	if err := os.Symlink(target, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	f, err := openACPFile(root, relative, write)
	if f != nil {
		_ = f.Close()
	}
	if err == nil {
		t.Fatalf("symlink followed: write=%v ancestor=%v", write, ancestor)
	}
}
