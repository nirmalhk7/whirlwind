package provider

import (
	"errors"
	"fmt"
)

func (s *providerSession) runCodex() error {
	if s.input.NativeIntegrations {
		s.output.SessionID = ""
	}
	if err := s.initializeCodex(); err != nil {
		return err
	}
	thread, params, err := s.openCodexThread()
	if err != nil {
		return err
	}
	inputs, catalogOnly, err := s.prepareCodexIntegrations(&thread, params)
	if err != nil || catalogOnly {
		return err
	}
	s.recordCodexSession(thread)
	return s.startCodexTurn(inputs)
}

func (s *providerSession) startCodexTurn(inputs []any) error {
	turn, err := s.codexTurnParams(inputs)
	if err != nil {
		return err
	}
	if err = s.rpc.Call(s.ctx, "turn/start", turn, nil); err != nil {
		return err
	}
	return s.waitCodexTurn()
}

func (s *providerSession) codexTurnParams(inputs []any) (map[string]any, error) {
	turn := map[string]any{"threadId": s.output.SessionID, "input": inputs}
	if s.input.NativeIntegrations && s.input.Thinking != nil {
		if s.input.Thinking.ConfigID != "reasoning_effort" {
			return nil, errors.New("native Codex integrations require reasoning_effort thinking option")
		}
		turn["effort"] = s.input.Thinking.Value
	}
	return turn, nil
}

func (s *providerSession) runClaude() error {
	var e error
	var response Output
	params := map[string]any{"cwd": s.input.CWD, "prompt": s.input.Prompt, "session_id": s.input.SessionID, "read_only": s.input.ReadOnly, "model": s.input.Model}
	params["native_integrations"] = s.input.NativeIntegrations
	params["provider_id"] = s.input.ProviderID
	params["discover_connected"] = s.input.DiscoverConnected
	params["catalog_only"] = s.input.CatalogOnly
	params["required_integrations"] = s.input.RequiredIntegrations
	if s.input.NativeIntegrations && s.input.Thinking != nil {
		params["effort"] = s.input.Thinking.Value
	}
	if len(s.checkServers) > 0 {
		server := s.checkServers[0].(map[string]any)
		params["check_mcp"] = map[string]any{"command": server["command"], "args": server["args"]}
	}
	e = s.rpc.Call(s.ctx, "run", params, &response)
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
