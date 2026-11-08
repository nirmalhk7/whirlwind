package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/testutil/openshell"
)

const providerLiveAcceptanceVariable = "WHIRLWIND_OPENSHELL_LIVE_ACCEPTANCE"

func newTerminalTestClient(t *testing.T, cwd string, live bool, hooks Hooks) (*Client, execution.Profile, func() error) {
	t.Helper()
	if !live {
		fixture := openshell.Install(t)
		fixture.EnableCommandExecution()
		t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
		t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "provider-terminal-test-image")
		t.Setenv("OPENSHELL_GATEWAY", "whirlwind-test-local")
		t.Setenv("OPENSHELL_GATEWAY_ENDPOINT", "http://127.0.0.1:8080")
		t.Setenv("OPENSHELL_WORKSPACE", "default")
	}
	canonicalCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := execution.ForPolicy(canonicalCWD, false, "provider-terminal-fixture", &model.SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile.AllowedPaths = []string{"**"}
	supervisorDir := filepath.Join(t.TempDir(), "supervisor", "standalone")
	profile, err = pinStandaloneProfile(context.Background(), profile, supervisorDir)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Input{CWD: canonicalCWD, Execution: &profile}, hooks)
	finished := false
	finish := func() error {
		if finished {
			return nil
		}
		finished = true
		if closeErr := client.Close(); closeErr != nil {
			return errors.Join(closeErr, execution.MarkStandaloneCleanupPending(profile))
		}
		if err := execution.ReconcileOpenShellRuntime(context.Background(), profile); err != nil {
			return errors.Join(err, execution.MarkStandaloneCleanupPending(profile))
		}
		return completeStandaloneProfile(profile)
	}
	t.Cleanup(func() {
		if err := finish(); err != nil {
			t.Errorf("finish OpenShell terminal fixture: %v", err)
		}
	})
	return client, profile, finish
}

func requireImagePathWritableBeforePolicy(t *testing.T, path string) {
	t.Helper()
	image := os.Getenv("WHIRLWIND_OPENSHELL_IMAGE")
	if image == "" {
		t.Fatal("WHIRLWIND_OPENSHELL_IMAGE is required for the live terminal boundary test")
	}
	command := exec.Command("docker", "run", "--rm", "--entrypoint", "/bin/sh", image,
		"-c", `test "$(id -u)" = 502 && test -w /home/whirlwind && touch "$1" && rm "$1"`, "probe", path)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("image-owned outside-worktree path is not writable without policy: %v: %s", err, output)
	}
}

func terminalProbePath() string {
	return fmt.Sprintf("/home/whirlwind/whirlwind-terminal-boundary-%d", time.Now().UnixNano())
}
