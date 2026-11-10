package provider

import (
	"errors"
	"whirlwind/internal/execution"
)

func (s *providerSession) setACPModes(sessionID string, modes []struct {
	ID string `json:"id"`
}) error {
	if s.usesOuterNonoSandbox() {
		return s.applyACPMode(sessionID, "agent-full-access", modes, true)
	}
	if s.input.ReadOnly {
		return s.applyACPMode(sessionID, "ask", modes, false)
	}
	return nil
}
func (s *providerSession) usesOuterNonoSandbox() bool {
	return s.input.ACPProfile == "codex" && s.input.Execution != nil && execution.IsNonoLauncher(s.input.Execution.Launcher)
}
func (s *providerSession) applyACPMode(sessionID, desired string, modes []struct {
	ID string `json:"id"`
}, required bool) error {
	for _, mode := range modes {
		if mode.ID == desired {
			return s.rpc.Call(s.ctx, "session/set_mode", map[string]any{"sessionId": sessionID, "modeId": desired}, nil)
		}
	}
	if required {
		return errors.New("codex ACP must expose agent-full-access mode to use the enforced nono sandbox")
	}
	return nil
}
