package provider

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/testutil/openshell"
)

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
