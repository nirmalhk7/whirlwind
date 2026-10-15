package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// clientMessage encodes fixture parameters as an ACP message for the real client handler.
func clientMessage(method string, v any) Message {
	b, _ := json.Marshal(v)
	return Message{Method: method, Params: b}
}

// TestACPFilesystemRespectsWorkspaceAndReadOnly verifies ranged reads work and symlink escapes and read-only writes are rejected.
func TestACPFilesystemRespectsWorkspaceAndReadOnly(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "read.txt"), []byte("one\ntwo\nthree"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	c := NewClient(Input{CWD: root}, Hooks{Permission: func(context.Context, Permission) (bool, error) { return true, nil }})
	defer c.Close()
	v, e := c.Handle(context.Background(), clientMessage("fs/read_text_file", map[string]any{"path": filepath.Join(root, "read.txt"), "line": 2, "limit": 1}))
	if e != nil || v.(map[string]any)["content"] != "two" {
		t.Fatalf(valueErrorFormat, v, e)
	}
	if _, e = c.Handle(context.Background(), clientMessage("fs/write_text_file", map[string]any{"path": filepath.Join(root, "escape", "secret"), "content": "bad"})); e == nil {
		t.Fatal("symlink escape allowed")
	}
	c.input.ReadOnly = true
	if _, e = c.Handle(context.Background(), clientMessage("fs/write_text_file", map[string]any{"path": filepath.Join(root, "new"), "content": "bad"})); e == nil {
		t.Fatal("read-only write allowed")
	}
}

// TestACPTerminalLifecycleAndOutputLimit verifies ACP terminals finish with an exit status and retain only the configured output tail.
func TestACPTerminalLifecycleAndOutputLimit(t *testing.T) {
	c := NewClient(Input{CWD: t.TempDir()}, Hooks{Permission: func(context.Context, Permission) (bool, error) { return true, nil }})
	defer c.Close()
	ctx := context.Background()
	v, e := c.Handle(ctx, clientMessage("terminal/create", map[string]any{"command": "sh", "args": []string{"-c", "printf 'abcdef'"}, "outputByteLimit": 3}))
	if e != nil {
		t.Fatal(e)
	}
	id := v.(map[string]any)["terminalId"]
	v, e = c.Handle(ctx, clientMessage("terminal/wait_for_exit", map[string]any{"terminalId": id}))
	if e != nil || v.(map[string]any)["exitCode"] != 0 {
		t.Fatalf(valueErrorFormat, v, e)
	}
	v, e = c.Handle(ctx, clientMessage("terminal/output", map[string]any{"terminalId": id}))
	if e != nil || v.(map[string]any)["output"] != "def" || v.(map[string]any)["truncated"] != true {
		t.Fatalf(valueErrorFormat, v, e)
	}
}
