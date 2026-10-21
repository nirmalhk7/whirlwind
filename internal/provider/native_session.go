package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (s *providerSession) runCodex() error {
	var e error
	initialization := map[string]any{"clientInfo": map[string]any{"name": "whirlwind", "version": "0.1.0"}}
	if s.input.Memory != nil {
		initialization["capabilities"] = map[string]bool{"experimentalApi": true}
	}
	e = s.rpc.Call(s.ctx, "initialize", initialization, nil)
	if e != nil {
		return e
	}
	_ = s.rpc.Notify("initialized", map[string]any{})
	quotaCtx, quotaCancel := context.WithTimeout(s.ctx, 5*time.Second)
	var quota json.RawMessage
	if s.rpc.Call(quotaCtx, "account/rateLimits/read", map[string]any{}, &quota) == nil && s.hooks.Event != nil {
		s.hooks.Event("whirlwind/quota", quota)
	}
	quotaCancel()
	params := map[string]any{"cwd": s.input.CWD, "approvalPolicy": "untrusted", "approvalsReviewer": "user", "sandbox": "workspace-write"}
	if s.input.Model != "" {
		params["model"] = s.input.Model
	}
	if s.input.ReadOnly {
		params["sandbox"] = "read-only"
	}
	if s.input.Memory != nil && s.input.SessionID == "" {
		params["dynamicTools"] = memoryToolDefinition()
	}
	method := "thread/start"
	if s.input.SessionID != "" {
		method = "thread/resume"
		params["threadId"] = s.input.SessionID
	}
	var thread struct {
		Model  string `json:"model"`
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	e = s.openNativeThread(method, params, &thread)
	if e != nil {
		return e
	}
	if thread.Thread.ID == "" {
		return errors.New("codex returned empty thread ID")
	}
	s.mu.Lock()
	s.output.SessionID = thread.Thread.ID
	s.output.ResolvedModel = thread.Model
	s.mu.Unlock()
	if s.hooks.Event != nil {
		s.hooks.Event("whirlwind/session", map[string]any{"session_id": s.output.SessionID, "resumed": s.input.SessionID != ""})
	}
	e = s.rpc.Call(s.ctx, "turn/start", map[string]any{"threadId": s.output.SessionID, "input": []any{map[string]any{"type": "text", "text": s.input.Prompt}}}, nil)
	if e != nil {
		return e
	}
	return s.waitCodexTurn()
}

func (s *providerSession) runClaude() error {
	var e error
	var response Output
	e = s.rpc.Call(s.ctx, "run", map[string]any{"cwd": s.input.CWD, "prompt": s.input.Prompt, "session_id": s.input.SessionID, "read_only": s.input.ReadOnly, "model": s.input.Model, "memory": s.input.Memory}, &response)
	s.mu.Lock()
	if e == nil {
		s.output = response
	} else {
		if response.SessionID != "" {
			s.output.SessionID = response.SessionID
		}
	}
	s.mu.Unlock()

	return e
}

func (s *providerSession) waitCodexTurn() error {
	var e error
	select {
	case status := <-s.completed:
		if status != "completed" {
			s.mu.Lock()
			e = s.turnFailure
			s.mu.Unlock()
			if e == nil {
				e = fmt.Errorf("%s turn %s", "Codex", status)
			}
		}
	case <-s.ctx.Done():
		e = s.ctx.Err()
	case <-s.rpc.done:
		e = s.rpc.closedError()
	}
	return e
}
