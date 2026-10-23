package execution

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/model"
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
