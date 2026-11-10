package execution

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/process"
)

// A stale OpenShell default or host-direct fallback breaks this real launch.
func TestDefaultNonoCommandRunsWithoutOpenShell(t *testing.T) {
	profile, err := ForPolicy(t.TempDir(), true, "policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(profile.Launcher), "/bridges/nono/") {
		t.Fatalf("default still requires another runtime: %s", profile.Launcher)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	profile.ReadRoots = append(profile.ReadRoots, node)
	cmd, err := Command(context.Background(), []string{node, "-e", "process.stdout.write('native-ready')"}, profile)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := process.Run(cmd); err != nil {
		t.Fatalf("native launch: %v: %s", err, output.String())
	}
	if output.String() != "native-ready" {
		t.Fatalf("unexpected protocol output: %q", output.String())
	}
}

func TestNonoMissingRuntimeFailsClosed(t *testing.T) {
	profile, err := ForPolicy(t.TempDir(), true, "policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WHIRLWIND_NONO_CLI", filepath.Join(t.TempDir(), "missing-nono"))
	marker := filepath.Join(profile.CWD, "escaped")
	cmd, err := Command(context.Background(), []string{"sh", "-c", "touch \"$1\"", "sh", marker}, profile)
	if err == nil {
		err = process.Run(cmd)
	}
	if err == nil {
		t.Fatal("missing nono executed command")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("target ran without sandbox: %v", err)
	}
}

func TestExplicitLegacyLauncherKeepsPersistedRuntimeRecovery(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "bridges", "openshell", "launch.mjs")
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", legacy)
	profile, err := ForPolicy(t.TempDir(), true, "policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Launcher != legacy {
		t.Fatalf("explicit runtime ignored: %s", profile.Launcher)
	}
}

// Installed Node adapters need their sibling runtime dependencies, not host home access.
func TestNonoProviderCanLoadInstalledNodeDependency(t *testing.T) {
	modules := filepath.Join(t.TempDir(), "node_modules")
	adapter := filepath.Join(modules, "adapter")
	dependency := filepath.Join(modules, "runtime")
	for _, dir := range []string{adapter, dependency} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"main":"index.js"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dependency, "index.js"), []byte(`module.exports='dependency-loaded'`), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(adapter, "index.js")
	if err := os.WriteFile(script, []byte(`process.stdout.write(require('runtime'))`), 0600); err != nil {
		t.Fatal(err)
	}
	command := []string{"node", script}
	profile, err := ForProvider(t.TempDir(), true, "policy", nil, command)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := Command(context.Background(), command, profile)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := process.Run(cmd); err != nil {
		t.Fatalf("Node runtime dependency denied: %v: %s", err, output.String())
	}
	if output.String() != "dependency-loaded" {
		t.Fatalf("dependency result: %s", output.String())
	}
}

func TestStandaloneNonoRejectsLegacyRuntimeKind(t *testing.T) {
	root := t.TempDir()
	profile := standaloneRegistryProfile(root, "kind-mismatch")
	profile.Launcher = "/app/bridges/nono/launch.mjs"
	if err := RegisterStandaloneRuntime(profile); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadStandaloneRuntimes(root)
	if err != nil {
		t.Fatal(err)
	}
	locator := rows[0]
	locator.RuntimeKind = "openshell"
	if _, err := StandaloneRuntimeProfile(root, locator); err == nil {
		t.Fatal("native launcher accepted legacy runtime authority")
	}
}
