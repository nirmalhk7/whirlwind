package provider

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"whirlwind/internal/model"
)

// Permission allow decisions must never widen the process filesystem boundary.
func TestACPTerminalCannotWriteOutsideSandbox(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "escaped")
	c := NewClient(Input{CWD: t.TempDir()}, Hooks{Permission: func(context.Context, Permission) (bool, error) { return true, nil }})
	defer c.Close()
	ctx := context.Background()
	v, err := c.Handle(ctx, clientMessage("terminal/create", map[string]any{"command": "/usr/bin/touch", "args": []string{outside}}))
	if err != nil {
		t.Fatal(err)
	}
	v, err = c.Handle(ctx, clientMessage("terminal/wait_for_exit", map[string]any{"terminalId": v.(map[string]any)["terminalId"]}))
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["exitCode"] == 0 {
		t.Fatal("outside write allowed")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatal("outside write occurred")
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
	c := NewClient(Input{CWD: root, Policy: model.Policy{AllowedPaths: []string{"allowed/**"}}}, Hooks{Permission: func(context.Context, Permission) (bool, error) { return true, nil }})
	defer c.Close()
	if _, err := c.Handle(context.Background(), clientMessage("fs/write_text_file", map[string]string{"path": "blocked.txt", "content": "bad"})); err == nil {
		t.Fatal("host callback escaped committed scope")
	}
	if _, err := os.Stat(filepath.Join(root, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatal("out-of-scope file created")
	}
}

func TestACPFilePermissionCannotRaceFilesystemBoundary(t *testing.T) {
	for _, target := range []string{"outside", ".git", "blocked"} {
		t.Run(target, func(t *testing.T) {
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
			c := NewClient(Input{CWD: root, Policy: model.Policy{AllowedPaths: []string{"allowed/**"}}}, Hooks{Permission: func(context.Context, Permission) (bool, error) {
				if err := os.Rename(allowed, filepath.Join(root, "previous")); err != nil {
					return false, err
				}
				return true, os.Symlink(protected, allowed)
			}})
			defer c.Close()
			_, err := c.Handle(context.Background(), clientMessage("fs/write_text_file", map[string]string{"path": "allowed/result.txt", "content": "escaped"}))
			if err == nil {
				t.Error("symlink swap during permission approval escaped filesystem boundary")
			}
			got, err := os.ReadFile(secret)
			if err != nil || string(got) != "unchanged" {
				t.Fatalf("protected target changed: %q %v", got, err)
			}
		})
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
	c := NewClient(Input{CWD: root, Policy: model.Policy{AllowedPaths: []string{"allowed/**"}}}, Hooks{Permission: func(context.Context, Permission) (bool, error) { return true, nil }})
	defer c.Close()
	_, err := c.Handle(context.Background(), clientMessage("fs/write_text_file", map[string]string{"path": "allowed/alias", "content": "escaped"}))
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
			root, outside := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("private"), 0600); err != nil {
				t.Fatal(err)
			}
			relative := "alias"
			target := filepath.Join(outside, "secret")
			if ancestor {
				target = outside
				relative = "alias/secret"
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
	}
}
