package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const runtimeHeartbeatInterval = 5 * time.Second
const runtimeLeaseLifetime = 20 * time.Second

var ErrRuntimeHealthLost = errors.New("sandbox runtime health lost")
var ErrMalformedRoleOutput = errors.New("malformed or missing role output")

func (a *agentAttempt) recordWorkerHeartbeat(ctx context.Context, at time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Finished {
		return fmt.Errorf("%w: attempt already finished", store.ErrConflict)
	}
	return a.updateRuntimeState(ctx, at, "")
}

func (a *agentAttempt) updateProcessState(ctx context.Context, state string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Finished {
		return store.ErrConflict
	}
	return a.updateRuntimeState(ctx, time.Now().UTC(), state)
}

func (a *agentAttempt) updateRuntimeState(ctx context.Context, at time.Time, processState string) error {
	now := time.Now().UTC()
	if err := validateHeartbeatSample(at, now); err != nil {
		return err
	}
	record, err := a.Store.Get(ctx, "sessions", a.id)
	if err != nil {
		return err
	}
	stored, err := store.Decode[model.Attempt](record)
	if err != nil {
		return err
	}
	if !sameRuntimeOwner(stored, a.state) {
		return fmt.Errorf("%w: runtime owner changed (finished=%t status=%s generation=%d expected=%d)", store.ErrConflict, stored.Finished, stored.Status, stored.ExecutionGeneration, a.state.ExecutionGeneration)
	}
	leaseExpires, err := time.Parse(time.RFC3339Nano, stored.LeaseExpiresAt)
	if err != nil || !now.Before(leaseExpires) {
		return fmt.Errorf("%w: runtime lease expired or invalid (expiry=%s observed=%s)", store.ErrConflict, stored.LeaseExpiresAt, now.Format(time.RFC3339Nano))
	}
	stored.WorkerHeartbeatAt = now.Format(time.RFC3339Nano)
	stored.LeaseExpiresAt = now.Add(runtimeLeaseLifetime).Format(time.RFC3339Nano)
	stored.ProviderProgressAt = a.state.ProviderProgressAt
	stored.ActiveTool = a.state.ActiveTool
	healthLost, err := updateSandboxState(&stored, now)
	if err != nil {
		return err
	}

	if processState != "" {
		stored.ProcessState = processState
	}
	if err := a.Store.Put(ctx, "sessions", a.id, stored, record.Version); err != nil {
		return fmt.Errorf("persist runtime heartbeat version %d: %w", record.Version, err)
	}
	a.state.WorkerHeartbeatAt = stored.WorkerHeartbeatAt
	a.state.LeaseExpiresAt = stored.LeaseExpiresAt
	a.state.ProviderProgressAt = stored.ProviderProgressAt
	a.state.ActiveTool = stored.ActiveTool
	a.state.SandboxID = stored.SandboxID
	a.state.SandboxPhase = stored.SandboxPhase
	a.state.SandboxHealth = stored.SandboxHealth
	a.state.ProcessState = stored.ProcessState
	if healthLost {
		return fmt.Errorf("%w: phase=%s health=%s", ErrRuntimeHealthLost, stored.SandboxPhase, stored.SandboxHealth)
	}
	return nil
}

func (a *agentAttempt) verifyLiveOwner(ctx context.Context) error {
	record, err := a.Store.Get(ctx, "sessions", a.id)
	if err != nil {
		return err
	}
	current, err := store.Decode[model.Attempt](record)
	if err != nil {
		return err
	}
	if !sameRuntimeOwner(current, a.state) || !runtimeLeaseActive(current, time.Now().UTC()) {
		return store.ErrConflict
	}
	return nil
}

func (a *agentAttempt) startWorkerHeartbeat(ctx context.Context) func() {
	return a.startWorkerHeartbeatAtInterval(ctx, runtimeHeartbeatInterval)
}

func (a *agentAttempt) startWorkerHeartbeatAtInterval(ctx context.Context, interval time.Duration) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case at := <-ticker.C:
				if err := a.recordWorkerHeartbeat(ctx, at); err != nil {
					if !errors.Is(err, ErrRuntimeHealthLost) {
						err = attemptPersistenceFailure(fmt.Errorf("worker heartbeat rejected: %w", err))
					}
					a.stopForAttemptError(err)
					return
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func providerProgress(attempt *model.Attempt, kind string, data any, at time.Time) {
	if kind != "session/update" {
		return
	}
	attempt.ProviderProgressAt = at.UTC().Format(time.RFC3339Nano)
	var event struct {
		Update struct {
			Kind   string `json:"sessionUpdate"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"update"`
	}
	encoded, err := json.Marshal(data)
	if err != nil || json.Unmarshal(encoded, &event) != nil {
		return
	}
	switch event.Update.Kind {
	case "tool_call":
		attempt.ActiveTool = event.Update.Name
	case "tool_call_update":
		if event.Update.Status == "completed" || event.Update.Status == "failed" || event.Update.Status == "cancelled" {
			attempt.ActiveTool = ""
		}
	}
}

func validateHeartbeatSample(at, now time.Time) error {
	if at.After(now.Add(time.Millisecond)) {
		return fmt.Errorf("%w: future heartbeat sample", store.ErrConflict)
	}
	if now.Sub(at) >= 2*runtimeHeartbeatInterval {
		return fmt.Errorf("%w: stale heartbeat sample (age %s)", store.ErrConflict, now.Sub(at))
	}
	return nil
}
