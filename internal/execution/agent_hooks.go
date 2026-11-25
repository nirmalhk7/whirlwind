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
	hookExecution := agentHookExecution{profile: profile, policy: projectPolicy, observe: observe}
	if !IsNonoLauncher(profile.Launcher) {
		return result, fmt.Errorf("%w: agent lifecycle hooks require native nono", ErrSandbox)
	}
	var feedback string
	for repair := 0; repair <= 3; repair++ {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if err = hookExecution.run(ctx, "before", hooks.Before); err != nil {
			return result, errors.Join(ErrAgentHookBlocked, err)
		}
		var commit func(context.Context) error
		if result, commit, err = round(ctx, feedback); err != nil {
			return result, hookRoundFailure(repair, err)
		}
		if err = hookExecution.run(ctx, "after", hooks.After); err == nil {
			return result, commitHookRound(ctx, commit)
		}
		var failure *hookFailure
		if !errors.As(err, &failure) {
			return result, errors.Join(ErrAgentHookBlocked, err)
		}
		feedback = failure.Error()
	}
	return result, errors.Join(ErrAgentHookBlocked, err)
}

type agentHookExecution struct {
	profile Profile
	policy  model.Policy
	observe func(string, error)
}

func (execution agentHookExecution) run(ctx context.Context, event string, checks []model.Check) error {
	if len(checks) == 0 {
		return nil
	}
	err := runAgentHook(ctx, execution.profile, execution.policy, event, checks)
	if execution.observe != nil {
		execution.observe(event, err)
	}
	return err
}

func hookRoundFailure(repair int, err error) error {
	if repair == 0 {
		return err
	}
	return errors.Join(ErrAgentHookBlocked, fmt.Errorf("continue agent after hook failure: %w", err))
}

func commitHookRound(ctx context.Context, commit func(context.Context) error) error {
	if err := ctx.Err(); err != nil || commit == nil {
		return err
	}
	return commit(ctx)
}

func runAgentHook(ctx context.Context, agent Profile, p model.Policy, event string, checks []model.Check) error {
	root, err := os.MkdirTemp("", "whirlwind-hook-snapshot-")
	if err != nil {
		return err
	}
	defer removeHookSnapshot(root)
	snapshot := filepath.Join(root, "source")
	identity, err := freezeHookSnapshot(ctx, agent.CWD, snapshot)
	if err != nil {
		return fmt.Errorf("freeze %s hook snapshot: %w", event, err)
	}
	profile, err := agentHookProfile(snapshot, agent, p, event, checks)
	if err != nil {
		return err
	}
	if err := runNativeAgentHook(ctx, profile, event); err != nil {
		return err
	}
	current, err := hookSnapshotIdentity(ctx, agent.CWD, nil)
	if err != nil || current != identity {
		return errors.Join(fmt.Errorf("worktree changed while sandbox %s hooks ran", event), err)
	}
	return nil
}

func removeHookSnapshot(root string) {
	if err := os.RemoveAll(root); err != nil {
		log.Printf("remove private hook snapshot: %v", err)
	}
}

func agentHookProfile(snapshot string, agent Profile, p model.Policy, event string, checks []model.Check) (Profile, error) {
	// Source grants are read-only. Provider credentials, network, brokers and
	// writable paths are deliberately absent from the hook sandbox.
	var policyReadRoots *model.SandboxPolicy
	if p.Sandbox != nil {
		policyReadRoots = &model.SandboxPolicy{ReadRoots: p.Sandbox.ReadRoots}
	}
	profile, err := ForPolicy(snapshot, true, agent.PolicyHash, policyReadRoots)
	if err != nil {
		return Profile{}, err
	}
	prepared, err := prepareAgentHookChecks(checks)
	if err != nil {
		return Profile{}, err
	}
	profile.Launcher = agent.Launcher
	profile.LifecycleHook = &lifecycleHook{Event: event, Checks: prepared}
	return profile, nil
}

func runNativeAgentHook(ctx context.Context, profile Profile, event string) error {
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
