package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"unicode/utf8"
)

type Client struct {
	input     Input
	hooks     Hooks
	mu        sync.Mutex
	terminals map[string]*terminal
	closed    bool
}
type terminal struct {
	mu         sync.Mutex
	buffer     []byte
	limit      int
	truncated  bool
	code       int
	signal     any
	done       chan struct{}
	cancel     context.CancelFunc
	id         string
	event      func(string, any)
	cleanupErr error
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
func (c *Client) Close() error {
	c.mu.Lock()
	c.closed = true
	terminals := []*terminal{}
	for _, term := range c.terminals {
		terminals = append(terminals, term)
		term.cancel()
	}
	c.mu.Unlock()
	var failures []error
	for _, term := range terminals {
		<-term.done
		failures = append(failures, term.cleanupErr)
	}
	return errors.Join(failures...)
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
