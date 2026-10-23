package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (a *agentAttempt) event(kind string, data any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.eventErr != nil {
		return
	}
	if err := a.saveEvent(kind, data); err != nil {
		var wait *AdmissionWait
		if errors.Is(err, store.ErrBudgetExceeded) || errors.As(err, &wait) {
			a.eventErr = err
		} else {
			a.eventErr = fmt.Errorf("%w: %v", store.ErrPersistence, err)
		}
	}
	if a.eventErr != nil && a.cancel != nil {
		a.cancel()
	}
}
func (a *agentAttempt) saveEvent(kind string, data any) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), 5*time.Second)
	defer cancel()
	b, err := store.SafeJSON(data)
	if err != nil {
		return err
	}
	replay := kind == "whirlwind/replay"
	if _, err = a.Store.Capture(ctx, a.id, kind, "application/json", b, store.MessagePayloadLimit, replay); err != nil {
		return err
	}
	if kind == "whirlwind/quota" {
		if err := a.quotaEvent(ctx, data); err != nil {
			return err
		}
		return a.checkLiveQuota(ctx, data)
	}
	if err := a.checkUsageEvent(ctx, kind, b); err != nil {
		return err
	}
	return a.saveStateEvent(ctx, kind, b)
}

func (a *agentAttempt) saveStateEvent(ctx context.Context, kind string, b []byte) error {
	var err error
	switch kind {
	case "whirlwind/config":
		err = a.applyConfigEvent(b)
	case "whirlwind/session":
		err = a.applySessionEvent(b)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	record, err := a.Store.Get(ctx, "sessions", a.id)
	if err != nil {
		return err
	}
	return a.Store.Put(ctx, "sessions", a.id, a.state, record.Version)
}

func (a *agentAttempt) applyConfigEvent(b []byte) error {
	var config struct {
		Model    string                `json:"resolved_model"`
		Thinking *model.ThinkingChoice `json:"effective_thinking"`
	}
	if err := json.Unmarshal(b, &config); err != nil {
		return err
	}
	if config.Model != "" {
		a.state.ResolvedModel = config.Model
	}
	if config.Thinking != nil {
		a.state.EffectiveThinking = config.Thinking
	}
	return nil
}

func (a *agentAttempt) applySessionEvent(b []byte) error {
	var current struct {
		ID      string `json:"session_id"`
		Resumed *bool  `json:"resumed"`
	}
	if err := json.Unmarshal(b, &current); err != nil {
		return err
	}
	a.state.SessionID = current.ID
	if current.Resumed != nil {
		a.state.Resumed = current.Resumed
	}
	return nil
}

func (a *agentAttempt) quotaEvent(ctx context.Context, data any) error {

	state, reset, err := provider.Quota(data)
	if err != nil {
		return nil
	}
	observation := map[string]any{"attempt_id": a.id, "provider": a.chosen.ID, "quota": state, "reset_at": reset, "observed_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if a.input.Purpose == "provider_test" {
		return a.Store.Put(ctx, "quota_observations", store.ID(), observation, 0)
	}
	record, err := a.Store.Get(ctx, "providers", a.chosen.ID)
	if err != nil {
		return err
	}
	value, err := store.Decode[model.Provider](record)
	if err != nil {
		return err
	}
	value.Quota = state
	value.ResetAt = reset
	return a.Store.Write(ctx, []store.Change{{Kind: "providers", ID: value.ID, Value: value, Expected: record.Version}, {Kind: "quota_observations", ID: store.ID(), Value: observation}}, nil)
}

func (a *agentAttempt) persistenceError(err error) error {
	if err == nil {
		return nil
	}
	wrapped := fmt.Errorf("%w: %v", store.ErrPersistence, err)
	a.mu.Lock()
	if a.eventErr == nil {
		a.eventErr = wrapped
	}
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return wrapped
}
