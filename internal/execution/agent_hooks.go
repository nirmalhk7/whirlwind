package execution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/process"
)

type lifecycleHook struct {
	Event  string        `json:"event"`
	Checks []model.Check `json:"checks"`
}

// ErrAgentHookBlocked prevents provider fallback from restarting a blocked hook run.
var ErrAgentHookBlocked = errors.New("agent lifecycle hook blocked completion")

type hookFailure struct{ event, diagnostics string }

func (e *hookFailure) Error() string {
	return fmt.Sprintf("sandbox %s hook failed: %s", e.event, e.diagnostics)
}

// RunAgentHooks owns the completion gate and the repair budget. A round must
// close the agent and its tools before returning; commit releases a staged
// side effect only for the round whose after checks passed.
func RunAgentHooks[T any](
	ctx context.Context, profile Profile, projectPolicy model.Policy, hooks model.AgentHookChecks,
	round func(context.Context, string) (T, func(context.Context) error, error),
	observe func(event string, err error),
) (result T, err error) {
	run := func(event string, checks []model.Check) error {
		if len(checks) == 0 {
			return nil
		}
		err := runAgentHook(ctx, profile, projectPolicy, event, checks)
		if observe != nil {
			observe(event, err)
		}
		return err
	}
	if !IsNonoLauncher(profile.Launcher) {
		return result, fmt.Errorf("%w: agent lifecycle hooks require native nono", ErrSandbox)
	}
	var feedback string
	for repair := 0; repair <= 3; repair++ {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if err = run("before", hooks.Before); err != nil {
			return result, errors.Join(ErrAgentHookBlocked, err)
		}
		var commit func(context.Context) error
		if result, commit, err = round(ctx, feedback); err != nil {
			if repair > 0 {
				err = errors.Join(ErrAgentHookBlocked, fmt.Errorf("continue agent after hook failure: %w", err))
			}
			return result, err
		}
		if err = run("after", hooks.After); err == nil {
			if err = ctx.Err(); err == nil && commit != nil {
				err = commit(ctx)
			}
			return result, err
		}
		var failure *hookFailure
		if !errors.As(err, &failure) {
			return result, errors.Join(ErrAgentHookBlocked, err)
		}
		feedback = failure.Error()
	}
	return result, errors.Join(ErrAgentHookBlocked, err)
}

func runAgentHook(ctx context.Context, agent Profile, p model.Policy, event string, checks []model.Check) error {
	root, err := os.MkdirTemp("", "whirlwind-hook-snapshot-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(root); err != nil {
			log.Printf("remove private hook snapshot: %v", err)
		}
	}()
	snapshot := filepath.Join(root, "source")
	identity, err := freezeHookSnapshot(ctx, agent.CWD, snapshot)
	if err != nil {
		return fmt.Errorf("freeze %s hook snapshot: %w", event, err)
	}
	// Source grants are read-only. Provider credentials, network, brokers and
	// writable paths are deliberately absent from the hook sandbox.
	var policyReadRoots *model.SandboxPolicy
	if p.Sandbox != nil {
		policyReadRoots = &model.SandboxPolicy{ReadRoots: p.Sandbox.ReadRoots}
	}
	profile, err := ForPolicy(snapshot, true, agent.PolicyHash, policyReadRoots)
	if err != nil {
		return err
	}
	prepared, err := prepareAgentHookChecks(checks)
	if err != nil {
		return err
	}
	profile.Launcher = agent.Launcher
	profile.LifecycleHook = &lifecycleHook{Event: event, Checks: prepared}
	// A dedicated native lifecycle runs after the previous provider and its
	// ACP terminals are fully reaped, including on each repair round.
	cmd, err := Command(ctx, []string{"/usr/bin/true"}, profile)
	if err != nil {
		return err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := process.Run(cmd); err != nil {
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), err)
		}
		var exit *exec.ExitError
		if !errors.Is(err, process.ErrCleanup) &&
			errors.As(err, &exit) && exit.ExitCode() == 86 {
			return &hookFailure{event: event, diagnostics: strings.TrimSpace(stdout.String())}
		}
		return fmt.Errorf("sandbox %s hook runner: %w: %s", event, err, strings.TrimSpace(stderr.String()))
	}
	current, err := hookSnapshotIdentity(ctx, agent.CWD, nil)
	if err != nil || current != identity {
		return errors.Join(fmt.Errorf("worktree changed while sandbox %s hooks ran", event), err)
	}
	return nil
}

func prepareAgentHookChecks(checks []model.Check) ([]model.Check, error) {
	prepared := slices.Clone(checks)
	for i := range prepared {
		check := &prepared[i]
		check.Command = slices.Clone(check.Command)
		if !strings.ContainsRune(check.Command[0], filepath.Separator) {
			target, err := commandTarget(check.Command[0])
			if err != nil {
				return nil, fmt.Errorf("hook %q executable: %w", check.Name, err)
			}
			check.Command[0] = target
		}
		for name, value := range check.Environment {
			// Values set on the approved check have the same authority as an
			// independent verification job's explicit environment.
			allowed := &model.SandboxPolicy{Environment: []string{name}}
			if !EnvironmentOverrideAllowed(name, allowed) || strings.ContainsRune(value, 0) {
				return nil, fmt.Errorf("hook %q environment variable %q is not allowed", check.Name, name)
			}
		}
	}
	return prepared, nil
}
