package workflow

import (
	"errors"
)

var ErrHumanDecisionPending = errors.New("human decision pending")

// A transport may expose a hook error as a tool response and keep sampling.
// Persisted human intent therefore revokes execution independently of whether
// the peer propagates the hook error to its top-level Run call.
func (a *agentAttempt) endForHumanDecision() error {
	a.mu.Lock()
	a.eventErr = errors.Join(a.eventErr, ErrHumanDecisionPending)
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return ErrHumanDecisionPending
}
