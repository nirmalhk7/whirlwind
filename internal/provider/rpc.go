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
	err         error
	once        sync.Once
	observe     func(string, any)
	diagnostics sync.WaitGroup
	callbacks   sync.WaitGroup
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
	return startCommand(ctx, cancel, cmd, handler, observers...)
}

func startCommand(ctx context.Context, cancel context.CancelFunc, cmd *exec.Cmd, handler Handler, observers ...func(string, any)) (*RPC, error) {
	pipes, e := rpcPipes(cmd)
	if e != nil {
		cancel()
		return nil, e
	}
	if e = process.Start(cmd); e != nil {
		cancel()
		return nil, e
	}
	r := &RPC{cmd: cmd, input: pipes.input, pending: map[string]chan Message{}, done: make(chan struct{}), ctx: ctx, cancel: cancel, handler: handler}
	if len(observers) > 0 {
		r.observe = observers[0]
	}
	r.diagnostics.Add(1)
	go r.readStderr(pipes.stderr)
	go r.read(pipes.output)
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
	v, e := r.handler(r.ctx, m)
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
	}
	_ = r.send(reply)
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
func (r *RPC) Close() { r.once.Do(func() { r.cancel(); _ = r.input.Close(); <-r.done }) }

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
			if start := strings.Index(diagnosticTail, "Sandbox setup/execution failed:"); start >= 0 {
				message := strings.SplitN(diagnosticTail[start:], "\n", 2)[0]
				r.mu.Lock()
				r.err = fmt.Errorf("%w: %s", execution.ErrSandbox, message)
				r.mu.Unlock()
			}
			if len(diagnosticTail) > 4096 {
				diagnosticTail = diagnosticTail[len(diagnosticTail)-4096:]
			}
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
