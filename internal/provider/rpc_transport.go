package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"whirlwind/internal/process"
)

type processPipes struct {
	input   io.WriteCloser
	output  io.ReadCloser
	stderr  io.ReadCloser
	writers []io.Closer
}

func rpcPipes(cmd *exec.Cmd) (processPipes, error) {
	input, err := cmd.StdinPipe()
	if err != nil {
		return processPipes{}, err
	}
	pipes := processPipes{input: input}
	if cmd.Stdout != nil || cmd.Stderr != nil {
		pipes.close()
		return processPipes{}, errors.New("provider output is already configured")
	}
	// Cmd.Wait closes StdoutPipe/StderrPipe readers. Own these readers instead,
	// so process cleanup cannot discard bytes before RPC diagnostics drain.
	output, outputWriter, err := os.Pipe()
	if err != nil {
		pipes.close()
		return processPipes{}, err
	}
	pipes.output, pipes.writers = output, []io.Closer{outputWriter}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		pipes.close()
		return processPipes{}, err
	}
	pipes.stderr = stderr
	pipes.writers = append(pipes.writers, stderrWriter)
	cmd.Stdout, cmd.Stderr = outputWriter, stderrWriter
	return pipes, nil
}

func (p processPipes) closeWriters() {
	for _, writer := range p.writers {
		_ = writer.Close()
	}
}

func (p processPipes) close() {
	p.closeWriters()
	for _, pipe := range []io.Closer{p.input, p.output, p.stderr} {
		if pipe != nil {
			_ = pipe.Close()
		}
	}
}

func (r *RPC) dispatchFrame(m Message) {
	if m.Method != "" {
		if len(m.ID) > 0 {
			r.callbacks.Add(1)
			go func() { defer r.callbacks.Done(); r.handle(m) }()
		} else {
			r.handle(m)
		}
		return
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

func (r *RPC) finishRead(e error) {
	r.mu.Lock()
	if r.err == nil {
		r.err = e
	}
	r.mu.Unlock()
	r.cancel()
	r.callbacks.Wait()
	release, exitErr := process.WaitForDrain(r.cmd)
	if release != nil && r.afterDrain != nil {
		if cleanupErr := r.afterDrain(); cleanupErr != nil {
			cleanupErr = errors.Join(process.ErrCleanup, fmt.Errorf("OpenShell sandbox cleanup failed: %w", cleanupErr))
			exitErr = errors.Join(exitErr, cleanupErr)
			release = nil
		}
	}
	r.mu.Lock()
	if errors.Is(exitErr, process.ErrCleanup) {
		r.cleanupErr = exitErr
	}
	if r.err == nil {
		r.err = exitErr
	}
	r.mu.Unlock()
	r.diagnostics.Wait()
	if release != nil {
		release()
	}
	if r.observe != nil {
		exit := map[string]any{"exit_code": r.cmd.ProcessState.ExitCode(), "cleanup_verified": r.cleanupErr == nil}
		if r.cleanupErr != nil {
			exit["cleanup_error"] = r.cleanupErr.Error()
		}
		if exitErr != nil {
			exit["error"] = exitErr.Error()
		}
		if r.err != nil {
			exit["protocol_error"] = r.err.Error()
		}
		r.observe("whirlwind/process_exit", exit)
	}
	close(r.done)
}

func (r *RPC) decodeResponse(method string, m Message, out any) error {
	if len(m.Error) > 0 {
		if r.observe != nil {
			r.observe("whirlwind/protocol_error", map[string]any{"method": method, "error": m.Error})
		}
		var failure RPCError
		if err := json.Unmarshal(m.Error, &failure); err != nil {
			return fmt.Errorf("provider RPC: %s: %w", m.Error, err)
		}
		return &failure
	}
	if out == nil {
		r.markReady(method)
		return nil
	}
	err := json.Unmarshal(m.Result, out)
	if err == nil {
		r.markReady(method)
	}
	return err
}

func (r *RPC) markReady(method string) {
	if method == "initialize" {
		r.mu.Lock()
		r.ready = true
		r.mu.Unlock()
	}
}

func (r *RPC) closedError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		var exit *exec.ExitError
		if !r.ready && errors.As(r.err, &exit) && (exit.ExitCode() == 126 || exit.ExitCode() == 127 || strings.Contains(r.stderrTail, "host is not on the allow list")) {
			return &TransportError{Cause: &StartupError{Cause: r.err, Diagnostic: r.stderrTail}}
		}
		return &TransportError{Cause: fmt.Errorf("provider exited before response: %w", r.err)}
	}
	return &TransportError{Cause: fmt.Errorf("provider exited before response: %w", io.ErrUnexpectedEOF)}
}
