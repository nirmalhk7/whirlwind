package execution

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/process"
)

// Omitting the selected version's package denies its entry point. Granting the
// versions parent leaks other installs; granting writes lets agents alter tools.
func TestNonoCursorLoadsOnlySelectedReadOnlyRuntime(t *testing.T) {
	const runtimeName = "cursor-agent"
	const ready = "runtime-ready"
	root := t.TempDir()
	version := filepath.Join(root, runtimeName, "versions", "selected")
	other := filepath.Join(root, runtimeName, "versions", "other", "secret")
	if err := os.MkdirAll(filepath.Dir(other), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(version, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"index.js": ready, "package.json": "{}", "node": "fixture", runtimeName: "#!/bin/sh\nset -eu\ncat \"$(dirname \"$0\")/index.js\"\nif cat \"$CURSOR_FIXTURE_OTHER\" 2>&1; then exit 40; fi\nif printf changed > \"$(dirname \"$0\")/index.js\"; then exit 41; fi\n"} {
		if err := os.WriteFile(filepath.Join(version, name), []byte(value), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(other, []byte("other-install-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	command := []string{filepath.Join(version, runtimeName)}
	profile, err := ForProvider(t.TempDir(), true, "policy", nil, command)
	if err != nil {
		t.Fatal(err)
	}
	profile.Environment = append(profile.Environment, "CURSOR_FIXTURE_OTHER")
	t.Setenv("CURSOR_FIXTURE_OTHER", other)
	cmd, err := Command(context.Background(), command, profile)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := process.Run(cmd); err != nil {
		t.Fatalf("selected runtime unavailable: %v: %s", err, output.String())
	}
	if !strings.HasPrefix(output.String(), ready) || strings.Contains(output.String(), "other-install-secret") {
		t.Fatalf("runtime read scope: %s", output.String())
	}
	if data, err := os.ReadFile(filepath.Join(version, "index.js")); err != nil || string(data) != ready {
		t.Fatalf("runtime changed: %s: %v", data, err)
	}
}
