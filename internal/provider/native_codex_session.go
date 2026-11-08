package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"whirlwind/internal/model"
)

type codexThread struct {
	Model  string `json:"model"`
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

func (s *providerSession) initializeCodex() error {
	initial := map[string]any{"clientInfo": map[string]any{"name": "whirlwind", "version": "0.1.0"}}
	if s.hasCheckTool() || s.input.NativeIntegrations {
		initial["capabilities"] = map[string]bool{"experimentalApi": true}
	}
	if err := s.rpc.Call(s.ctx, "initialize", initial, nil); err != nil {
		return err
	}
	_ = s.rpc.Notify("initialized", map[string]any{})
	s.readCodexQuota()
	return nil
}

func (s *providerSession) readCodexQuota() {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	var quota json.RawMessage
	if s.rpc.Call(ctx, "account/rateLimits/read", map[string]any{}, &quota) == nil && s.hooks.Event != nil {
		s.hooks.Event("whirlwind/quota", quota)
	}
}

func (s *providerSession) codexThreadParams() map[string]any {
	params := map[string]any{"cwd": s.input.CWD, "approvalPolicy": "untrusted", "approvalsReviewer": "user", "sandbox": "workspace-write"}
	if tools := s.checkTools(); len(tools) > 0 && (s.input.SessionID == "" || s.input.NativeIntegrations) {
		params["dynamicTools"] = tools
	}
	if s.input.Model != "" {
		params["model"] = s.input.Model
	}
	if s.input.ReadOnly {
		params["sandbox"] = "read-only"
	}
	return params
}

func (s *providerSession) openCodexThread() (codexThread, map[string]any, error) {
	params := s.codexThreadParams()
	method := "thread/start"
	if s.input.SessionID != "" && !s.input.NativeIntegrations {
		method = "thread/resume"
		params["threadId"] = s.input.SessionID
	}
	if s.input.NativeIntegrations {
		// Loaded-thread resume ignores startup configuration overrides. Discovery
		// uses an inference-free ephemeral probe; execution starts cold with policy.
		params["ephemeral"] = true
	}
	var thread codexThread
	if err := s.rpc.Call(s.ctx, method, params, &thread); err != nil {
		return thread, params, err
	}
	if thread.Thread.ID == "" {
		return thread, params, errors.New("codex returned empty thread ID")
	}
	return thread, params, nil
}

func (s *providerSession) prepareCodexIntegrations(thread *codexThread, params map[string]any) ([]any, bool, error) {
	inputs := []any{map[string]any{"type": "text", "text": s.input.Prompt}}
	if !s.input.NativeIntegrations {
		return inputs, false, nil
	}
	catalog, err := s.discoverCodexIntegrations(thread.Thread.ID)
	if err != nil {
		return nil, false, err
	}
	s.output.Integrations = catalog
	if err = s.checkIntegrationInventory(catalog); err != nil {
		return nil, false, err
	}
	if s.input.CatalogOnly {
		return nil, true, nil
	}
	err = s.startNativeCodexThread(thread, params, catalog)
	return s.integrationInputs(catalog), false, err
}

func (s *providerSession) startNativeCodexThread(thread *codexThread, params map[string]any, catalog []model.Integration) error {
	params["config"] = s.nativeIntegrationPolicy(catalog)
	delete(params, "ephemeral")
	probeID := thread.Thread.ID
	if err := s.rpc.Call(s.ctx, "thread/start", params, thread); err != nil {
		return fmt.Errorf("apply native integration permissions on new thread: %w", err)
	}
	if thread.Thread.ID == "" || thread.Thread.ID == probeID {
		return errors.New("codex did not create a fresh policy-bound execution thread")
	}
	return nil
}

func (s *providerSession) recordCodexSession(thread codexThread) {
	s.mu.Lock()
	s.output.SessionID = thread.Thread.ID
	s.output.ResolvedModel = thread.Model
	s.mu.Unlock()
	if s.hooks.Event != nil {
		s.hooks.Event("whirlwind/session", map[string]any{"session_id": s.output.SessionID, "resumed": s.input.SessionID != "" && !s.input.NativeIntegrations})
	}
}
