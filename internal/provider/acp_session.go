package provider

import (
	"errors"
	"fmt"
	"whirlwind/internal/model"
)

func (s *providerSession) runACP() error {
	session, e := s.openACPSession()
	if e != nil {
		return e
	}
	s.mu.Lock()
	s.output.SessionID = session.ID
	s.mu.Unlock()
	if s.hooks.Event != nil {
		s.hooks.Event("whirlwind/session", map[string]any{"session_id": session.ID, "resumed": s.input.SessionID != ""})
	}
	if e = s.configureACP(&session); e != nil {
		return e
	}
	var result struct {
		Stop string `json:"stopReason"`
	}
	e = s.rpc.Call(s.ctx, "session/prompt", map[string]any{"sessionId": session.ID, "prompt": []any{map[string]any{"type": "text", "text": s.input.Prompt}}}, &result)
	if s.hooks.Event != nil {
		s.hooks.Event("whirlwind/stop", map[string]string{"stop_reason": result.Stop})
	}
	if e == nil && result.Stop != "end_turn" {
		e = fmt.Errorf("ACP stopped: %s", result.Stop)
	}

	return e
}

func (s *providerSession) configureACP(session *acpSession) error {
	if err := s.setACPModes(session.ID, session.Modes.Available); err != nil {
		return err
	}
	if err := s.setACPModel(session); err != nil {
		return err
	}
	if err := s.setACPThinking(session); err != nil {
		return err
	}
	s.recordACPConfig(session.ConfigOptions, session.Models.Current)
	return nil
}

func (s *providerSession) initializeACP() (bool, error) {
	var init struct {
		Version      int `json:"protocolVersion"`
		Capabilities struct {
			Load bool `json:"loadSession"`
		} `json:"agentCapabilities"`
	}
	e := s.rpc.Call(s.ctx, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": true, "writeTextFile": !s.input.ReadOnly}, "terminal": !s.input.ReadOnly}, "clientInfo": map[string]any{"name": "whirlwind", "version": "0.1.0"}}, &init)
	if e != nil {
		return false, e
	}
	if init.Version != 1 {
		return false, fmt.Errorf("unsupported ACP protocol version: %d", init.Version)
	}
	return init.Capabilities.Load, nil
}

func (s *providerSession) setACPModes(sessionID string, modes []struct {
	ID string `json:"id"`
}) error {
	var e error
	if s.input.ReadOnly {
		for _, mode := range modes {
			if mode.ID == "ask" {
				if e = s.rpc.Call(s.ctx, "session/set_mode", map[string]any{"sessionId": sessionID, "modeId": "ask"}, nil); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

func (s *providerSession) recordACPConfig(options []acpConfigOption, legacyModel string) {
	session := acpSession{ConfigOptions: options}
	session.Models.Current = legacyModel
	catalog, _ := session.modelCatalog()
	s.mu.Lock()
	if catalog.Current != "" {
		s.output.ResolvedModel = catalog.Current
	}
	for _, option := range session.thinkingOptions() {
		if s.input.Thinking == nil || s.input.Thinking.ConfigID == option.ConfigID {
			s.output.EffectiveThinking = &model.ThinkingChoice{ConfigID: option.ConfigID, Value: option.Current}
			break
		}
	}
	metadata := map[string]any{"resolved_model": s.output.ResolvedModel, "effective_thinking": s.output.EffectiveThinking}
	s.mu.Unlock()
	if s.hooks.Event != nil {
		s.hooks.Event("whirlwind/config", metadata)
	}
}

func (s *providerSession) openACPSession() (acpSession, error) {
	var session acpSession
	load, err := s.initializeACP()
	if err != nil {
		return session, err
	}
	if s.input.SessionID != "" && !load {
		return session, errors.New("unsupported ACP session resume: loadSession is false")
	}
	if s.input.SessionID != "" {
		session.ID = s.input.SessionID
		s.replaying.Store(true)
		err = s.rpc.Call(s.ctx, "session/load", map[string]any{"sessionId": session.ID, "cwd": s.input.CWD, "mcpServers": s.memoryServers()}, &session)
		s.replaying.Store(false)
		s.mu.Lock()
		s.output.Text = ""
		s.mu.Unlock()
	} else {
		err = s.rpc.Call(s.ctx, "session/new", map[string]any{"cwd": s.input.CWD, "mcpServers": s.memoryServers()}, &session)
	}
	if err != nil {
		return session, err
	}
	if session.ID == "" {
		return session, errors.New("ACP returned empty session ID")
	}
	return session, nil
}
