package workflow

import (
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

func updateSandboxState(stored *model.Attempt, now time.Time) (bool, error) {

	if stored.LifecycleFile == "" {
		return false, nil
	}
	{
		lifecycle, exists, err := execution.ReadRuntimeLifecycle(execution.Profile{
			ExecutionID: stored.ExecutionID, ExecutionGeneration: stored.ExecutionGeneration,
			DeadlineAt: stored.DeadlineAt, ExecutionLabelValue: stored.ExecutionLabelValue,
			SandboxName: stored.SandboxName, LifecycleFile: stored.LifecycleFile,
			GatewayName: stored.GatewayName, GatewayEndpoint: stored.GatewayEndpoint,
			Workspace: stored.Workspace,
		})
		if err != nil {
			return false, err
		}
		if exists {
			stored.SandboxID = lifecycle.SandboxID
			stored.SandboxPhase = lifecycle.SandboxPhase
			if runtimeLifecycleFresh(stored.RuntimeKind, lifecycle.UpdatedAt, now) {
				stored.SandboxHealth = lifecycle.SandboxHealth
			} else {
				stored.SandboxHealth = "stale"
			}
		} else {
			stored.SandboxHealth = "unknown"
		}
		shuttingDown := stored.SandboxHealth == "stopping" || stored.SandboxHealth == "stopped"
		return stored.SandboxID != "" && !shuttingDown && (stored.SandboxPhase != "Ready" || stored.SandboxHealth != "healthy"), nil
	}
}
func runtimeLifecycleFresh(kind, updated string, now time.Time) bool {
	if kind == "nono" {
		return true
	}
	at, err := time.Parse(time.RFC3339Nano, updated)
	return err == nil && now.Sub(at) >= 0 && now.Sub(at) <= 2*runtimeHeartbeatInterval
}
