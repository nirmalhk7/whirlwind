package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"

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
	cmd     *exec.Cmd
	input   io.WriteCloser
	write   sync.Mutex
	mu      sync.Mutex
	pending map[string]chan Message
	next    atomic.Int64
	done    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	handler Handler
	err     error
	once    sync.Once
}

// Start starts a registered provider process and initializes newline-delimited JSON-RPC transport with a background reader.
func Start(ctx context.Context, command []string, cwd string, handler Handler) (*RPC, error) {
	if len(command) == 0 {
		return nil, errors.New("provider command required")
	}
	ctx, cancel := context.WithCancel(ctx)
	cmd := process.Command(ctx, command[0], command[1:]...)
	cmd.Dir = cwd
	input, e := cmd.StdinPipe()
	if e != nil {
		cancel()
		return nil, e
	}
	output, e := cmd.StdoutPipe()
	if e != nil {
		cancel()
		return nil, e
	}
	stderr, e := cmd.StderrPipe()
	if e != nil {
		cancel()
		return nil, e
	}
	if e = process.Start(cmd); e != nil {
		cancel()
		return nil, e
	}
	r := &RPC{cmd: cmd, input: input, pending: map[string]chan Message{}, done: make(chan struct{}), ctx: ctx, cancel: cancel, handler: handler}
	go io.Copy(io.Discard, stderr)
	go r.read(output)
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
		if m.Method != "" {
			if len(m.ID) > 0 {
				go r.handle(m)
			} else {
				r.handle(m)
			}
			continue
		}
		r.mu.Lock()
		ch := r.pending[string(m.ID)]
		r.mu.Unlock()
		if ch != nil {
			select {
			case ch <- m:
			case <-r.ctx.Done():
			}
		}
	}
	e := scanner.Err()
	r.mu.Lock()
	if r.err == nil {
		r.err = e
	}
	r.mu.Unlock()
	r.cancel()
	_ = r.cmd.Wait()
	process.Forget(r.cmd)
	close(r.done)
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
		return e
	}
	select {
	case m := <-ch:
		if len(m.Error) > 0 {
			return fmt.Errorf("provider RPC: %s", m.Error)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(m.Result, out)
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.err != nil {
			return r.err
		}
		return errors.New("provider exited before response")
	}
}

// Close cancels the provider, closes its input, and waits for reader cleanup exactly once.
func (r *RPC) Close() { r.once.Do(func() { r.cancel(); _ = r.input.Close(); <-r.done }) }
