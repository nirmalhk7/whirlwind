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
	id        string
	event     func(string, any)
}

// NewClient creates the ACP filesystem and terminal handler for one worktree and its permission hooks.
func NewClient(input Input, hooks Hooks) *Client {
	return &Client{input: input, hooks: hooks, terminals: map[string]*terminal{}}
}

// Handle serves ACP file reads/writes and terminal lifecycle requests, enforcing workspace bounds, permissions, and output limits.
func (c *Client) Handle(ctx context.Context, m Message) (any, error) {
	var p clientParams
	if e := json.Unmarshal(m.Params, &p); e != nil {
		return nil, e
	}
	handlers := map[string]func(context.Context, Message, clientParams) (any, error){
		"fs/read_text_file":      c.readFile,
		"fs/write_text_file":     c.writeFile,
		"terminal/create":        c.createTerminal,
		"terminal/output":        c.handleTerminal,
		"terminal/wait_for_exit": c.handleTerminal,
		"terminal/kill":          c.handleTerminal,
		"terminal/release":       c.handleTerminal,
	}
	if handler, ok := handlers[m.Method]; ok {
		return handler(ctx, m, p)
	}
	return nil, fmt.Errorf("unsupported ACP client method: %s", m.Method)
}

// Write retains the newest terminal output up to its byte limit, trimming partial UTF-8 characters and recording truncation.
func (t *terminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	n := len(p)
	t.buffer = append(t.buffer, p...)
	if len(t.buffer) > t.limit {
		t.buffer = t.buffer[len(t.buffer)-t.limit:]
		t.truncated = true
		for len(t.buffer) > 0 && !utf8.RuneStart(t.buffer[0]) {
			t.buffer = t.buffer[1:]
		}
	}
	t.mu.Unlock()
	if t.event != nil {
		t.event("whirlwind/terminal/outputDelta", map[string]any{"terminalId": t.id, "delta": string(p)})
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

type clientParams struct {
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

func (c *Client) readFile(ctx context.Context, m Message, p clientParams) (any, error) {

	path, e := c.path(p.Path)
	if e != nil {
		return nil, e
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
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

}

func (c *Client) writeFile(ctx context.Context, m Message, p clientParams) (any, error) {

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

}

func (term *terminal) wait(cmd *exec.Cmd) {

	defer close(term.done)
	defer process.Forget(cmd)
	err := cmd.Wait()
	term.mu.Lock()
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
		term.event("whirlwind/terminal/exit", map[string]any{"terminalId": term.id, "exitCode": code, "signal": signal})
	}
}

func (c *Client) createTerminal(ctx context.Context, m Message, p clientParams) (any, error) {
	cwd, err := c.authorizeTerminal(ctx, m, p)
	if err != nil {
		return nil, err
	}
	run, cancel := context.WithCancel(ctx)
	cmd := process.Command(run, p.Command, p.Args...)
	cmd.Dir = cwd
	if err := p.applyEnvironment(cmd); err != nil {
		cancel()
		return nil, err
	}
	limit := p.OutputByteLimit
	if limit <= 0 || limit > 1024*1024 {
		limit = 1024 * 1024
	}
	id := store.ID()
	term := &terminal{limit: limit, done: make(chan struct{}), cancel: cancel, id: id, event: c.hooks.Event}
	cmd.Stdout = term
	cmd.Stderr = term
	if e := process.Start(cmd); e != nil {
		cancel()
		return nil, e
	}
	c.mu.Lock()
	c.terminals[id] = term
	c.mu.Unlock()
	go term.wait(cmd)
	return map[string]any{"terminalId": id}, nil

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

func (p clientParams) applyEnvironment(cmd *exec.Cmd) error {
	for _, env := range p.Env {
		if strings.HasPrefix(env.Name, "WHIRLWIND_PROCESS_") {
			return errors.New("reserved process environment")
		}
		cmd.Env = append(cmd.Env, env.Name+"="+env.Value)
	}
	return nil
}

func (c *Client) handleTerminal(ctx context.Context, m Message, p clientParams) (any, error) {

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
		return term.waitForExit(ctx)
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
}

func (term *terminal) waitForExit(ctx context.Context) (any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-term.done:
		term.mu.Lock()
		defer term.mu.Unlock()
		return map[string]any{"exitCode": term.code, "signal": term.signal}, nil
	}
}
