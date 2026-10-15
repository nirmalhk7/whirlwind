package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"whirlwind/internal/process"
)

type processPipes struct {
	input  io.WriteCloser
	output io.ReadCloser
	stderr io.ReadCloser
}

func rpcPipes(cmd *exec.Cmd) (processPipes, error) {
	input, err := cmd.StdinPipe()
	if err != nil {
		return processPipes{}, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return processPipes{}, err
	}
	stderr, err := cmd.StderrPipe()
	return processPipes{input: input, output: output, stderr: stderr}, err
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
	exitErr := r.cmd.Wait()
	r.diagnostics.Wait()
	if r.observe != nil {
		exit := map[string]any{"exit_code": r.cmd.ProcessState.ExitCode()}
		if exitErr != nil {
			exit["error"] = exitErr.Error()
		}
		if r.err != nil {
			exit["protocol_error"] = r.err.Error()
		}
		r.observe("whirlwind/process_exit", exit)
	}
	process.Forget(r.cmd)
	close(r.done)
}

func (r *RPC) decodeResponse(method string, m Message, out any) error {
	if len(m.Error) > 0 {
		if r.observe != nil {
			r.observe("whirlwind/protocol_error", map[string]any{"method": method, "error": m.Error})
		}
		return fmt.Errorf("provider RPC: %s", m.Error)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(m.Result, out)
}

func (r *RPC) closedError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	return errors.New("provider exited before response")
}
