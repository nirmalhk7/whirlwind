package provider

import (
	"context"
	"time"
)

func (s *providerSession) stopIfRequested() {
	if s.hooks.ShouldStop == nil || !s.hooks.ShouldStop() || s.rpc == nil || s.stopRequested.Swap(true) {
		return
	}
	go s.requestGracefulStop()
}

func (s *providerSession) requestGracefulStop() {
	s.mu.Lock()
	session, turn := s.output.SessionID, s.turnID
	s.mu.Unlock()
	switch s.transport {
	case "acp":
		_ = s.rpc.Notify("session/cancel", map[string]string{"sessionId": session})
	case "codex":
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		defer cancel()
		_ = s.rpc.Call(ctx, "turn/interrupt", map[string]string{"threadId": session, "turnId": turn}, nil)
	case "claude":
		_ = s.rpc.Notify("cancel", map[string]string{})
	}
	s.awaitGracefulStop()
}

func (s *providerSession) awaitGracefulStop() {
	select {
	case <-s.rpc.done:
	case <-s.ctx.Done():
	case <-time.After(5 * time.Second):
		s.runtimeEvent("whirlwind/recovery_uncertain", map[string]string{"reason": "provider did not acknowledge stop"})
		s.rpc.cancel()
	}
}
