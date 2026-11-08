package provider

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/process"
	"whirlwind/internal/store"
)

func (term *terminal) wait(cmd *exec.Cmd) {
	defer close(term.done)
	err := process.Wait(cmd)
	term.mu.Lock()
	if errors.Is(err, process.ErrCleanup) {
		term.cleanupErr = err
	}
	term.code = 0
	if err != nil {
		term.code = -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			term.code = exit.ExitCode()
			if exit.ProcessState != nil && term.code < 0 {
				term.signal = exit.String()
			}
		}
	}
	code, signal := term.code, term.signal
	term.mu.Unlock()
	if term.event != nil {
		term.event("whirlwind/terminal/exit", map[string]any{"terminalId": term.id, "exitCode": code, "signal": signal, "cleanup_verified": term.cleanupErr == nil})
	}
}

func (c *Client) createTerminal(ctx context.Context, m Message, p clientParams) (any, error) {
	cwd, err := c.authorizeTerminal(ctx, m, p)
	if err != nil {
		return nil, err
	}
	id, err := c.startTerminal(ctx, p, cwd)
	if err != nil {
		return nil, err
	}
	return map[string]any{"terminalId": id}, nil
}

func (c *Client) startTerminal(ctx context.Context, p clientParams, cwd string) (string, error) {
	run, cancel := context.WithCancel(ctx)
	cmd, err := c.prepareTerminalCommand(run, p, cwd, cancel)
	if err != nil {
		return "", err
	}
	term := c.newTerminal(p.OutputByteLimit, cancel)
	cmd.Stdout, cmd.Stderr = term, term
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		cancel()
		return "", context.Canceled
	}
	if err = process.Start(cmd); err != nil {
		cancel()
		return "", err
	}
	c.terminals[term.id] = term
	go term.wait(cmd)
	return term.id, nil
}

func (c *Client) prepareTerminalCommand(run context.Context, p clientParams, cwd string, cancel context.CancelFunc) (*exec.Cmd, error) {
	profile, err := c.executionProfile()
	if err != nil {
		cancel()
		return nil, err
	}
	profile.CWD = cwd
	cmd, err := execution.Command(run, append([]string{p.Command}, p.Args...), profile)
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Dir = cwd
	if err = p.applyEnvironment(cmd, c.input.Policy.Sandbox); err != nil {
		cancel()
		return nil, err
	}
	return cmd, nil
}

func (c *Client) newTerminal(outputLimit int, cancel context.CancelFunc) *terminal {
	limit := outputLimit
	if limit <= 0 || limit > 1024*1024 {
		limit = 1024 * 1024
	}
	return &terminal{limit: limit, done: make(chan struct{}), cancel: cancel, id: store.ID(), event: c.hooks.Event}
}

func (c *Client) authorizeTerminal(ctx context.Context, m Message, p clientParams) (string, error) {
	if p.Command == "" {
		return "", errors.New("terminal command required")
	}
	cwd := c.input.CWD
	if p.CWD != "" {
		var err error
		cwd, err = c.path(p.CWD)
		if err != nil {
			return "", err
		}
	}
	args := append([]string{p.Command}, p.Args...)
	err := c.allow(ctx, Permission{Tool: m.Method, Command: args, Raw: m.Params})
	return cwd, err
}

func (p clientParams) applyEnvironment(cmd *exec.Cmd, policies ...*model.SandboxPolicy) error {
	var policy *model.SandboxPolicy
	if len(policies) > 0 {
		policy = policies[0]
	}
	for _, env := range p.Env {
		if !execution.EnvironmentOverrideAllowed(env.Name, policy) {
			return errors.New("reserved process environment")
		}
		cmd.Env = append(cmd.Env, env.Name+"="+env.Value)
	}
	return nil
}

func (c *Client) handleTerminal(ctx context.Context, m Message, p clientParams) (any, error) {
	term := c.terminal(p.TerminalID)
	if term == nil {
		return nil, errors.New("terminal not found")
	}
	switch m.Method {
	case "terminal/output":
		return term.output(), nil
	case "terminal/wait_for_exit":
		return term.waitForExit(ctx)
	default:
		return c.stopTerminal(ctx, m.Method, p.TerminalID, term)
	}
}

func (c *Client) terminal(id string) *terminal {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.terminals[id]
}

func (term *terminal) output() map[string]any {
	term.mu.Lock()
	defer term.mu.Unlock()
	v := map[string]any{"output": strings.ToValidUTF8(string(term.buffer), ""), "truncated": term.truncated}
	select {
	case <-term.done:
		v["exitStatus"] = map[string]any{"exitCode": term.code, "signal": term.signal}
	default:
	}
	return v
}

func (c *Client) stopTerminal(ctx context.Context, method, id string, term *terminal) (any, error) {
	term.cancel()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-term.done:
	}
	if term.cleanupErr != nil {
		return nil, term.cleanupErr
	}
	if method == "terminal/release" {
		c.mu.Lock()
		delete(c.terminals, id)
		c.mu.Unlock()
	}
	return map[string]any{}, nil
}

func (term *terminal) waitForExit(ctx context.Context) (any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-term.done:
		term.mu.Lock()
		defer term.mu.Unlock()
		if term.cleanupErr != nil {
			return nil, term.cleanupErr
		}
		return map[string]any{"exitCode": term.code, "signal": term.signal}, nil
	}
}
