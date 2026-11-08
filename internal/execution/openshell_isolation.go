package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type sandboxIdentity struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Workspace string            `json:"workspace"`
	Labels    map[string]string `json:"labels"`
	Phase     string            `json:"phase"`
	ExitCode  *int              `json:"exit_code"`
}

type sandboxPage struct {
	Sandboxes     []sandboxIdentity `json:"sandboxes"`
	NextPageToken string            `json:"next_page_token"`
}

// ReconcileOpenShellRuntime proves the pinned execution resource is absent before
// allowing a successor runtime to write into the same worktree.
func ReconcileOpenShellRuntime(ctx context.Context, profile Profile) error {
	if err := validateIsolationIdentity(profile); err != nil {
		return err
	}
	if profile.ExecutionLabelValue == "" {
		profile.ExecutionLabelValue = OpenShellExecutionLabel(profile.ExecutionID, profile.ExecutionGeneration)
	}
	cli, err := openShellCLI()
	if err != nil {
		return isolationFailure(err)
	}
	sandbox, found, err := findPinnedSandbox(ctx, cli, profile)
	if err != nil || !found {
		return err
	}
	if err := validateSandboxIdentity(sandbox, profile); err != nil {
		return err
	}
	if profile.SandboxID == "" {
		profile.SandboxID = sandbox.ID
	}
	if profile.StandaloneRuntime {
		if err := PinStandaloneSandboxID(profile, sandbox.ID); err != nil {
			return err
		}
	}
	if !confirmedSandboxIsolation(sandbox) {
		stopErr := runSandboxSuccessful(ctx, cli, sandboxCommand(profile, "stop"))
		stopped, observeErr := runSandboxJSON(ctx, cli, sandboxCommand(profile, "get", profile.SandboxName, "--output", "json"))
		if observeErr != nil {
			return isolationFailure(errors.Join(stopErr, observeErr))
		}
		if err := validateSandboxIdentity(stopped, profile); err != nil {
			return err
		}
		if stopErr != nil && !confirmedSandboxIsolation(stopped) {
			return isolationFailure(stopErr)
		}
		sandbox = stopped
	}
	if !confirmedSandboxIsolation(sandbox) {
		return fmt.Errorf("%w: OpenShell phase %q did not confirm process isolation", ErrIsolationUnverified, sandbox.Phase)
	}
	if err := runSandboxSuccessful(ctx, cli, sandboxCommand(profile, "delete")); err != nil {
		// A lost delete response is reconciled by listing the exact pinned authority.
		stillPresent, found, listErr := findPinnedSandbox(ctx, cli, profile)
		if listErr != nil || found {
			return isolationFailure(errors.Join(err, listErr, identityErrorIfPresent(stillPresent, found, profile)))
		}
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		stillPresent, found, err := findPinnedSandbox(ctx, cli, profile)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if err := validateSandboxIdentity(stillPresent, profile); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return isolationFailure(ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("%w: sandbox deletion did not complete", ErrIsolationUnverified)
		case <-ticker.C:
		}
	}
}

func runSandboxSuccessful(ctx context.Context, cli string, args []string) error {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := exec.CommandContext(callCtx, cli, args...).Output(); err != nil {
		return fmt.Errorf("OpenShell %s failed: %w", strings.Join(args[:min(2, len(args))], " "), err)
	}
	return nil
}

func validateIsolationIdentity(profile Profile) error {
	if !validExecutionID(profile.ExecutionID) || profile.ExecutionGeneration < 1 || profile.SandboxName == "" || profile.GatewayName == "" || profile.GatewayEndpoint == "" || profile.Workspace == "" {
		return fmt.Errorf("%w: persisted OpenShell identity is incomplete", ErrIsolationUnverified)
	}
	if profile.SandboxName != OpenShellSandboxName(profile.ExecutionID) || profile.ExecutionLabelValue != "" && profile.ExecutionLabelValue != OpenShellExecutionLabel(profile.ExecutionID, profile.ExecutionGeneration) {
		return fmt.Errorf("%w: sandbox name or execution label is not bound to the persisted attempt generation", ErrIsolationUnverified)
	}
	if !loopbackEndpoint(profile.GatewayEndpoint) {
		return fmt.Errorf("%w: persisted OpenShell authority is not local", ErrIsolationUnverified)
	}
	return nil
}

func findPinnedSandbox(ctx context.Context, cli string, profile Profile) (sandboxIdentity, bool, error) {
	pageToken := ""
	for {
		args := sandboxCommand(profile, "list", "--output", "json", "--selector", "whirlwind.io/execution="+profile.ExecutionLabelValue)
		if pageToken != "" {
			args = append(args, "--page-token", pageToken)
		}
		var page sandboxPage
		if err := runSandboxJSONInto(ctx, cli, args, &page); err != nil {
			return sandboxIdentity{}, false, isolationFailure(err)
		}
		for _, sandbox := range page.Sandboxes {
			if sandbox.Name == profile.SandboxName {
				return sandbox, true, nil
			}
		}
		if page.NextPageToken == "" {
			return sandboxIdentity{}, false, nil
		}
		pageToken = page.NextPageToken
	}
}

func sandboxCommand(profile Profile, operation string, args ...string) []string {
	command := []string{"sandbox", operation}
	if operation == "stop" || operation == "delete" {
		if len(args) == 0 || args[0] != profile.SandboxName {
			args = append([]string{profile.SandboxName}, args...)
		}
	}
	command = append(command, args...)
	command = append(command, "--gateway", profile.GatewayName, "--gateway-endpoint", profile.GatewayEndpoint, "--workspace", profile.Workspace)
	return command
}

func runSandboxJSON(ctx context.Context, cli string, args []string) (sandboxIdentity, error) {
	var value sandboxIdentity
	err := runSandboxJSONInto(ctx, cli, args, &value)
	return value, err
}

func runSandboxJSONInto(ctx context.Context, cli string, args []string, output any) error {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := exec.CommandContext(callCtx, cli, args...).Output()
	if err != nil {
		return fmt.Errorf("OpenShell %s failed: %w", strings.Join(args[:min(2, len(args))], " "), err)
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("OpenShell returned invalid JSON: %w", err)
	}
	return nil
}

func validateSandboxIdentity(sandbox sandboxIdentity, profile Profile) error {
	if sandbox.Name != profile.SandboxName || sandbox.Workspace != profile.Workspace || sandbox.ID == "" || profile.SandboxID != "" && sandbox.ID != profile.SandboxID || sandbox.Labels["whirlwind.io/execution"] != profile.ExecutionLabelValue {
		return fmt.Errorf("%w: discovered OpenShell sandbox does not match execution ID, generation, workspace, and resource ID", ErrIsolationUnverified)
	}
	return nil
}

func confirmedSandboxIsolation(sandbox sandboxIdentity) bool {
	return sandbox.Phase == "Stopped" || (sandbox.Phase == "Completed" || sandbox.Phase == "Error") && sandbox.ExitCode != nil
}

func identityErrorIfPresent(sandbox sandboxIdentity, found bool, profile Profile) error {
	if !found {
		return nil
	}
	return validateSandboxIdentity(sandbox, profile)
}

func isolationFailure(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrIsolationUnverified, err)
}
