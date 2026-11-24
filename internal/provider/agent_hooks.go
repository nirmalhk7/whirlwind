package provider

import (
	"context"
	"errors"
	"fmt"
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
	var profile execution.Profile
	if in.Execution != nil {
		profile = *in.Execution
	} else {
		profile, err = execution.ForPolicy(in.CWD, in.ReadOnly, "", in.Policy.Sandbox)
		if err != nil {
			return Output{}, err
		}
	}
	if len(checks.After) > 0 && profile.RuntimeDir == "" {
		dir, err := os.MkdirTemp("", "whirlwind-hook-session-")
		if err != nil {
			return Output{}, err
		}
		defer os.RemoveAll(dir)
		in.hookRuntimeDir = dir
	}
	sessionID := in.SessionID
	result, err := execution.RunAgentHooks(ctx, profile, in.Policy, checks, func(ctx context.Context, feedback string) (Output, func(context.Context) error, error) {
		input := in
		input.SessionID = sessionID
		if feedback != "" {
			if sessionID == "" {
				return Output{}, nil, errors.New("cannot repair hook failure without the original ACP session")
			}
			input.Prompt = "Repair the following sandbox check failure within the original task scope. The sandbox will rerun the checks automatically. Return the complete response required by the original task.\n\n" + feedback
			if h.Event != nil {
				h.Event("whirlwind/agent_hook_repair", map[string]string{"diagnostics": feedback, "session_id": sessionID})
			}
		}
		roundHooks, pending := stageHookedFinalization(h, len(checks.After) > 0)
		out, runErr := a.runResolved(ctx, p, input, roundHooks)
		if runErr != nil {
			return out, nil, runErr
		}
		if sessionID != "" && out.SessionID != sessionID {
			return out, nil, fmt.Errorf("sandbox repair reopened a different ACP session")
		}
		sessionID = out.SessionID
		return out, pending(out), nil
	}, func(event string, err error) {
		if h.Event == nil {
			return
		}
		status := map[string]any{"event": event, "passed": err == nil}
		if err != nil {
			status["error"] = err.Error()
		}
		h.Event("whirlwind/agent_hook", status)
	})
	if err != nil {
		result.Text = ""
	}
	return result, err
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
				return errors.New("Finalizer returned no summary for staged delivery")
			}
			_, err := original(ctx)
			return err
		}
	}
}
