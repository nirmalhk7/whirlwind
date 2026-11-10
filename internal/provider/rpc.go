package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/process"
)

type Message struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}
type Handler func(context.Context, Message) (any, error)
type RPC struct {
	cmd         *exec.Cmd
	input       io.WriteCloser
	write       sync.Mutex
	mu          sync.Mutex
	pending     map[string]chan Message
	next        atomic.Int64
	done        chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
	handler     Handler
	afterDrain  func() error
	err         error
	cleanupErr  error
	once        sync.Once
	observe     func(string, any)
	diagnostics sync.WaitGroup
	callbacks   sync.WaitGroup
	ready       bool
	stderrTail  string
}

// Start starts a registered provider process and initializes newline-delimited JSON-RPC transport with a background reader.
func Start(ctx context.Context, command []string, cwd string, handler Handler, observers ...func(string, any)) (*RPC, error) {
	if len(command) == 0 {
		return nil, errors.New("provider command required")
	}
	ctx, cancel := context.WithCancel(ctx)
	cmd := process.Command(ctx, command[0], command[1:]...)
	cmd.Dir = cwd
	return startCommand(ctx, cancel, cmd, handler, observers...)
}

// StartWithProfile is the enforced provider/terminal execution entry point.
func StartWithProfile(ctx context.Context, command []string, profile execution.Profile, handler Handler, observers ...func(string, any)) (*RPC, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd, err := execution.Command(ctx, command, profile)
	if err != nil {
		cancel()
		return nil, err
	}
	var cleanup func() error
	if execution.IsNonoLauncher(profile.Launcher) {
		// RPC invokes this only after WaitForDrain has verified this process tree.
		// Native nono has no detached VM resource; lifecycle callbacks run after
		// stderr drains, together with capability release.
		cleanup = func() error { return completeStandaloneProfile(profile) }
	} else if execution.IsOpenShellLauncher(profile.Launcher) {
		cleanup = func() error {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			if err := execution.ReconcileRuntime(cleanupCtx, profile); err != nil {
				return retainStandaloneProfile(profile, err)
			}
			return completeStandaloneProfile(profile)
		}
	}
	return startCommandWithCleanup(ctx, cancel, cmd, handler, cleanup, observers...)
}

func startCommand(ctx context.Context, cancel context.CancelFunc, cmd *exec.Cmd, handler Handler, observers ...func(string, any)) (*RPC, error) {
	return startCommandWithCleanup(ctx, cancel, cmd, handler, nil, observers...)
}

func startCommandWithCleanup(ctx context.Context, cancel context.CancelFunc, cmd *exec.Cmd, handler Handler, afterDrain func() error, observers ...func(string, any)) (*RPC, error) {
	pipes, e := rpcPipes(cmd)
	if e != nil {
		cancel()
		return nil, errors.Join(e, process.Forget(cmd))
	}
	if e = process.Start(cmd); e != nil {
		pipes.close()
		cancel()
		return nil, e
	}
	pipes.closeWriters()
	r := &RPC{cmd: cmd, input: pipes.input, pending: map[string]chan Message{}, done: make(chan struct{}), ctx: ctx, cancel: cancel, handler: handler, afterDrain: afterDrain}
	if len(observers) > 0 {
		r.observe = observers[0]
	}
	r.diagnostics.Add(1)
	go func() {
		defer pipes.stderr.Close()
		r.readStderr(pipes.stderr)
	}()
	go func() {
		defer pipes.output.Close()
		r.read(pipes.output)
	}()
	return r, nil
}

// read decodes provider frames, dispatches callbacks, routes replies to waiting calls, and cancels and reaps the process on stream termination.
func (r *RPC) read(output io.Reader) {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	for scanner.Scan() {
		var m Message
		if e := json.Unmarshal(scanner.Bytes(), &m); e != nil {
			r.mu.Lock()
			r.err = fmt.Errorf("malformed provider frame: %w", e)
			r.mu.Unlock()
			r.cancel()
			break
		}
		r.dispatchFrame(m)
	}
	r.finishRead(scanner.Err())
}

// handle runs the callback handler and replies to provider requests, returning protocol errors when handling fails or is unsupported.
func (r *RPC) handle(m Message) {
	if r.handler == nil {
		if len(m.ID) > 0 {
			_ = r.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "unsupported method"}})
		}
		return
	}
	trackAction := requestHasSideEffect(m)
	if trackAction && r.observe != nil {
		r.observe("whirlwind/action_started", map[string]any{"action_id": string(m.ID), "method": m.Method})
	}
	var v any
	var e error
	if trackAction && r.ctx != nil && r.ctx.Err() != nil {
		e = r.ctx.Err()
	} else {
		v, e = r.handler(r.ctx, m)
	}
	if len(m.ID) == 0 {
		return
	}
	reply := map[string]any{"jsonrpc": "2.0", "id": m.ID}
	if e != nil {
		reply["error"] = map[string]any{"code": -32603, "message": e.Error()}
	} else {
		reply["result"] = v
	}
	if r.observe != nil {
		r.observe("whirlwind/tool_result", map[string]any{"method": m.Method, "request_id": m.ID, "reply": reply})
		if trackAction {
			r.observe("whirlwind/action_result", actionResult(m, v, e))
		}
	}
	if trackAction && r.ctx != nil && r.ctx.Err() != nil {
		reply = map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32603, "message": r.ctx.Err().Error()}}
	}
	_ = r.send(reply)
}

func requestHasSideEffect(message Message) bool {
	if len(message.ID) == 0 {
		return false
	}
	switch message.Method {
	case "fs/write_text_file", "terminal/create", "terminal/kill", "terminal/release", "tools/call", "item/tool/call", "whirlwind/run_check":
		return true
	default:
		return false
	}
}

func actionResult(message Message, result any, err error) map[string]any {
	status := "completed"
	if err != nil {
		status = "failed"
	}
	record := map[string]any{"action_id": string(message.ID), "method": message.Method, "status": status}
	if message.Method == "terminal/create" && err == nil {
		if response, ok := result.(map[string]any); ok {
			if terminalID, ok := response["terminalId"].(string); ok {
				record["terminal_id"] = terminalID
			}
		}
	}
	return record
}

// send serializes writes so concurrent RPC messages cannot interleave on provider stdin.
func (r *RPC) send(v any) error {
	r.write.Lock()
	defer r.write.Unlock()
	return json.NewEncoder(r.input).Encode(v)
}

// Notify sends a JSON-RPC notification without allocating a response waiter.
func (r *RPC) Notify(method string, params any) error {
	return r.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// Call sends an identified RPC request and waits for its decoded result, protocol error, cancellation, or provider exit.
func (r *RPC) Call(ctx context.Context, method string, params any, out any) error {
	id := fmt.Sprint(r.next.Add(1))
	ch := make(chan Message, 1)
	r.mu.Lock()
	r.pending[id] = ch
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.pending, id); r.mu.Unlock() }()
	if e := r.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method, "params": params}); e != nil {
		return &TransportError{Cause: e}
	}
	select {
	case m := <-ch:
		return r.decodeResponse(method, m, out)
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return r.closedError()
	}
}

// Close cancels the provider, closes its input, and waits for reader cleanup exactly once.
func (r *RPC) Close() error {
	r.once.Do(func() { r.cancel(); _ = r.input.Close(); <-r.done })
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cleanupErr
}

// Drain stderr throughout execution; detailed output is bounded independently of process lifetime.
func (r *RPC) readStderr(reader io.Reader) {
	defer r.diagnostics.Done()
	var seen, stored int64
	var diagnosticTail string
	buffer := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			diagnosticTail += string(buffer[:n])
			captureSandboxDiagnostic(r, diagnosticTail)
			diagnosticTail = boundedDiagnosticTail(diagnosticTail)
			r.mu.Lock()
			r.stderrTail = diagnosticTail
			r.mu.Unlock()
			seen += int64(n)
			retain := min(n, max(0, 1024*1024-int(stored)))
			if retain > 0 && r.observe != nil {
				r.observe("whirlwind/stderr", map[string]any{"text": string(buffer[:retain])})
				stored += int64(retain)
			}
		}
		if err != nil {
			break
		}
	}
	if seen > stored && r.observe != nil {
		r.observe("whirlwind/stderr_truncated", map[string]any{"bytes": seen, "stored_bytes": stored})
	}
}

func captureSandboxDiagnostic(r *RPC, diagnostic string) {
	start := strings.Index(diagnostic, "Sandbox setup/execution failed:")
	if start < 0 {
		return
	}
	message := strings.SplitN(diagnostic[start:], "\n", 2)[0]
	r.mu.Lock()
	r.err = fmt.Errorf("%w: %s", execution.ErrSandbox, message)
	r.mu.Unlock()
}

func boundedDiagnosticTail(diagnostic string) string {
	if len(diagnostic) > 4096 {
		return diagnostic[len(diagnostic)-4096:]
	}
	return diagnostic
}
