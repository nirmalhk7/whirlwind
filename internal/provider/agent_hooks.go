package provider

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
)

func (a ProcessAdapter) runWithAgentHooks(ctx context.Context, p model.Provider, in Input, h Hooks) (Output, error) {
	checks, err := policy.ResolveAgentHooks(in.AgentHooks)
	if err != nil {
		return Output{}, errors.Join(execution.ErrAgentHookBlocked, err)
	}
	profile, err := agentHookExecution(in)
	if err != nil {
		return Output{}, err
	}
	if len(checks.After) > 0 && profile.RuntimeDir == "" {
		dir, err := os.MkdirTemp("", "whirlwind-hook-session-")
		if err != nil {
			return Output{}, err
		}
		defer func() {
			if err := os.RemoveAll(dir); err != nil {
				log.Printf("remove private hook session: %v", err)
			}
		}()
		in.hookRuntimeDir = dir
	}
	run := providerHookRun{adapter: a, provider: p, input: in, hooks: h, deferDelivery: len(checks.After) > 0, sessionID: in.SessionID}
	result, err := execution.RunAgentHooks(ctx, profile, in.Policy, checks, run.round, run.observe)
	if err != nil {
		result.Text = ""
	}
	return result, err
}

func agentHookExecution(in Input) (execution.Profile, error) {
	if in.Execution != nil {
		return *in.Execution, nil
	}
	return execution.ForPolicy(in.CWD, in.ReadOnly, "", in.Policy.Sandbox)
}

type providerHookRun struct {
	adapter       ProcessAdapter
	provider      model.Provider
	input         Input
	hooks         Hooks
	deferDelivery bool
	sessionID     string
}

func (run *providerHookRun) round(ctx context.Context, feedback string) (Output, func(context.Context) error, error) {
	input := run.input
	input.SessionID = run.sessionID
	if feedback != "" {
		if run.sessionID == "" {
			return Output{}, nil, errors.New("cannot repair hook failure without the original ACP session")
		}
		input.Prompt = "Repair the following sandbox check failure within the original task scope. The sandbox will rerun the checks automatically. Return the complete response required by the original task.\n\n" + feedback
		if run.hooks.Event != nil {
			run.hooks.Event("whirlwind/agent_hook_repair", map[string]string{"diagnostics": feedback, "session_id": run.sessionID})
		}
	}
	hooks, pending := stageHookedFinalization(run.hooks, run.deferDelivery)
	output, err := run.adapter.runResolved(ctx, run.provider, input, hooks)
	if err != nil {
		return output, nil, err
	}
	if run.sessionID != "" && output.SessionID != run.sessionID {
		return output, nil, fmt.Errorf("sandbox repair reopened a different ACP session")
	}
	run.sessionID = output.SessionID
	return output, pending(output), nil
}

func (run *providerHookRun) observe(event string, err error) {
	if run.hooks.Event == nil {
		return
	}
	status := map[string]any{"event": event, "passed": err == nil}
	if err != nil {
		status["error"] = err.Error()
	}
	run.hooks.Event("whirlwind/agent_hook", status)
}

func stageHookedFinalization(h Hooks, deferred bool) (Hooks, func(Output) func(context.Context) error) {
	original := h.FinalizeDelivery
	var requested atomic.Bool
	if deferred && original != nil {
		h.FinalizeDelivery = func(ctx context.Context) (FinalizationResult, error) {
			if err := ctx.Err(); err != nil {
				return FinalizationResult{}, err
			}
			requested.Store(true)
			return FinalizationResult{Pending: true}, nil
		}
	}
	return h, func(result Output) func(context.Context) error {
		if !requested.Load() {
			return nil
		}
		return func(ctx context.Context) error {
			if strings.TrimSpace(result.Text) == "" {
				return errors.New("finalizer returned no summary for staged delivery")
			}
			_, err := original(ctx)
			return err
		}
	}
}
