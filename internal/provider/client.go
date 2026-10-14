package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"whirlwind/internal/process"
	"whirlwind/internal/store"
)

type Client struct {
	input     Input
	hooks     Hooks
	mu        sync.Mutex
	terminals map[string]*terminal
}
type terminal struct {
	mu        sync.Mutex
	buffer    []byte
	limit     int
	truncated bool
	code      int
	signal    any
	done      chan struct{}
	cancel    context.CancelFunc
}

// NewClient creates the ACP filesystem and terminal handler for one worktree and its permission hooks.
func NewClient(input Input, hooks Hooks) *Client {
	return &Client{input: input, hooks: hooks, terminals: map[string]*terminal{}}
}

// path resolves symlinks and nonexistent path suffixes while rejecting worktree escapes and direct Git metadata access.
func (c *Client) path(requested string) (string, error) {
	root, e := filepath.EvalSymlinks(c.input.CWD)
	if e != nil {
		return "", e
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)
	existing := candidate
	suffix := []string{}
	for {
		resolved, e := filepath.EvalSymlinks(existing)
		if e == nil {
			candidate = filepath.Join(append([]string{resolved}, suffix...)...)
			break
		}
		if !os.IsNotExist(e) {
			return "", e
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", e
		}
		suffix = append([]string{filepath.Base(existing)}, suffix...)
		existing = parent
	}
	relative, e := filepath.Rel(root, candidate)
	if e != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("ACP path escapes worktree")
	}
	if relative == ".git" || strings.HasPrefix(relative, ".git/") {
		return "", errors.New("ACP direct Git metadata access denied")
	}
	return candidate, nil
}

// allow rejects mutations for read-only roles and asks the configured hook to authorize a tool request.
func (c *Client) allow(ctx context.Context, p Permission) error {
	if c.input.ReadOnly {
		return errors.New("read-only agent role")
	}
	if c.hooks.Permission == nil {
		return errors.New("permission handler unavailable")
	}
	ok, e := c.hooks.Permission(ctx, p)
	if e != nil {
		return e
	}
	if !ok {
		return errors.New("tool permission denied")
	}
	return nil
}

// Handle serves ACP file reads/writes and terminal lifecycle requests, enforcing workspace bounds, permissions, and output limits.
func (c *Client) Handle(ctx context.Context, m Message) (any, error) {
	var p struct {
		Path            string   `json:"path"`
		Content         string   `json:"content"`
		Line            int      `json:"line"`
		Limit           int      `json:"limit"`
		Command         string   `json:"command"`
		Args            []string `json:"args"`
		CWD             string   `json:"cwd"`
		OutputByteLimit int      `json:"outputByteLimit"`
		TerminalID      string   `json:"terminalId"`
		Env             []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"env"`
	}
	if e := json.Unmarshal(m.Params, &p); e != nil {
		return nil, e
	}
	switch m.Method {
	case "fs/read_text_file":
		path, e := c.path(p.Path)
		if e != nil {
			return nil, e
		}
		f, e := os.Open(path)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		b, e := io.ReadAll(io.LimitReader(f, 1024*1024+1))
		if e != nil {
			return nil, e
		}
		if len(b) > 1024*1024 {
			return nil, errors.New("file exceeds 1 MiB")
		}
		lines := strings.Split(string(b), "\n")
		start := min(max(p.Line-1, 0), len(lines))
		end := len(lines)
		if p.Limit > 0 && start+p.Limit < end {
			end = start + p.Limit
		}
		return map[string]any{"content": strings.Join(lines[start:end], "\n")}, nil
	case "fs/write_text_file":
		path, e := c.path(p.Path)
		if e != nil {
			return nil, e
		}
		if len(p.Content) > 1024*1024 {
			return nil, errors.New("file content exceeds 1 MiB")
		}
		if e = c.allow(ctx, Permission{Tool: m.Method, Paths: []string{path}, Raw: m.Params}); e != nil {
			return nil, e
		}
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return nil, e
		}
		e = os.WriteFile(path, []byte(p.Content), 0600)
		return map[string]any{}, e
	case "terminal/create":
		if p.Command == "" {
			return nil, errors.New("terminal command required")
		}
		cwd := c.input.CWD
		if p.CWD != "" {
			var e error
			cwd, e = c.path(p.CWD)
			if e != nil {
				return nil, e
			}
		}
		args := append([]string{p.Command}, p.Args...)
		if e := c.allow(ctx, Permission{Tool: m.Method, Command: args, Raw: m.Params}); e != nil {
			return nil, e
		}
		run, cancel := context.WithCancel(ctx)
		cmd := process.Command(run, p.Command, p.Args...)
		cmd.Dir = cwd
		for _, env := range p.Env {
			if strings.HasPrefix(env.Name, "WHIRLWIND_PROCESS_") {
				cancel()
				return nil, errors.New("reserved process environment")
			}
			cmd.Env = append(cmd.Env, env.Name+"="+env.Value)
		}
		limit := p.OutputByteLimit
		if limit <= 0 || limit > 1024*1024 {
			limit = 1024 * 1024
		}
		term := &terminal{limit: limit, done: make(chan struct{}), cancel: cancel}
		cmd.Stdout = term
		cmd.Stderr = term
		if e := process.Start(cmd); e != nil {
			cancel()
			return nil, e
		}
		id := store.ID()
		c.mu.Lock()
		c.terminals[id] = term
		c.mu.Unlock()
		go func() {
			defer close(term.done)
			defer process.Forget(cmd)
			err := cmd.Wait()
			term.mu.Lock()
			defer term.mu.Unlock()
			term.code = 0
			if err != nil {
				term.code = -1
				var exit *exec.ExitError
				if errors.As(err, &exit) {
					term.code = exit.ExitCode()
					if exit.ProcessState != nil && term.code < 0 {
						term.signal = exit.ProcessState.String()
					}
				}
			}
		}()
		return map[string]any{"terminalId": id}, nil
	case "terminal/output", "terminal/wait_for_exit", "terminal/kill", "terminal/release":
		c.mu.Lock()
		term := c.terminals[p.TerminalID]
		c.mu.Unlock()
		if term == nil {
			return nil, errors.New("terminal not found")
		}
		switch m.Method {
		case "terminal/output":
			term.mu.Lock()
			v := map[string]any{"output": strings.ToValidUTF8(string(term.buffer), ""), "truncated": term.truncated}
			select {
			case <-term.done:
				v["exitStatus"] = map[string]any{"exitCode": term.code, "signal": term.signal}
			default:
			}
			term.mu.Unlock()
			return v, nil
		case "terminal/wait_for_exit":
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-term.done:
				term.mu.Lock()
				defer term.mu.Unlock()
				return map[string]any{"exitCode": term.code, "signal": term.signal}, nil
			}
		default:
			term.cancel()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-term.done:
			}
			if m.Method == "terminal/release" {
				c.mu.Lock()
				delete(c.terminals, p.TerminalID)
				c.mu.Unlock()
			}
			return map[string]any{}, nil
		}
	default:
		return nil, fmt.Errorf("unsupported ACP client method: %s", m.Method)
	}
}

// Write retains the newest terminal output up to its byte limit, trimming partial UTF-8 characters and recording truncation.
func (t *terminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	t.buffer = append(t.buffer, p...)
	if len(t.buffer) > t.limit {
		t.buffer = t.buffer[len(t.buffer)-t.limit:]
		t.truncated = true
		for len(t.buffer) > 0 && !utf8.RuneStart(t.buffer[0]) {
			t.buffer = t.buffer[1:]
		}
	}
	return n, nil
}

// Close cancels all managed terminal processes and waits for their exit.
func (c *Client) Close() {
	c.mu.Lock()
	terminals := []*terminal{}
	for _, term := range c.terminals {
		terminals = append(terminals, term)
		term.cancel()
	}
	c.mu.Unlock()
	for _, term := range terminals {
		<-term.done
	}
}
