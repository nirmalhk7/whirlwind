package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
	execprofile "whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (a *agentRun) newAttempt(chosen model.Provider) (_ *agentAttempt, resultErr error) {
	execution, err := a.prepareNewAttempt(chosen)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			if err := a.recordPrelaunchFailure(execution, chosen, resultErr); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("%w: prelaunch cleanup: %v", store.ErrPersistence, err))
			}
		}
	}()
	if resultErr = execution.launch(); resultErr != nil {
		return nil, resultErr
	}
	return execution, nil
}

func (a *agentRun) prepareNewAttempt(chosen model.Provider) (*agentAttempt, error) {
	if err := a.reconcilePriorToolActions(a.ctx); err != nil {
		return nil, err
	}
	if err := a.prepareExecutionProfile(chosen); err != nil {
		return nil, err
	}
	if err := a.reconcileOpenShellAttempts(a.ctx); err != nil {
		return nil, err
	}
	if a.input.Role == "code" {
		if err := a.recordCodingOrigin(a.ctx, a.task, a.input.CWD, a.input.UnitID); err != nil {
			return nil, err
		}
	}
	if err := a.prepareAttemptResume(chosen); err != nil {
		return nil, err
	}
	execution := &agentAttempt{agentRun: a, chosen: chosen, id: store.ID()}
	if _, err := a.reserveAttempt(chosen, execution.id); err != nil {
		return nil, err
	}
	return execution, nil
}

func (execution *agentAttempt) launch() error {
	a := execution.agentRun
	chosen := execution.chosen
	started := time.Now().UTC()
	episodeID := recoveryEntryID(recoveryEpisodeID(a), model.AgentChoice{Provider: chosen.ID, Model: a.input.Model, Thinking: a.input.Thinking})
	generation, err := nextExecutionGeneration(a.ctx, a.Store, episodeID)
	if err != nil {
		return err
	}
	deadline := started.Add(a.attemptTimeoutLimit())
	if profile := a.input.Execution; profile != nil && execprofile.IsOpenShellLauncher(profile.Launcher) {
		pinned, err := execprofile.PinOpenShellRuntime(a.ctx, *profile, execution.id, generation, deadline.Format(time.RFC3339Nano), filepath.Join(a.Root, "supervisor", "executions"))
		if err != nil {
			return err
		}
		a.input.Execution = &pinned
	}
	configHash := a.input.ExecutionProfile
	runtimeKind, runtimeLauncher := attemptRuntimeKind(a.input.Execution)
	if a.input.Execution != nil {
		configHash = a.input.Execution.Digest()
	}
	execution.state = model.Attempt{ExecutionID: execution.id, ExecutionGeneration: generation, RuntimeKind: runtimeKind, RuntimeLauncher: runtimeLauncher, WorkerHeartbeatAt: started.Format(time.RFC3339Nano), LeaseExpiresAt: started.Add(runtimeLeaseLifetime).Format(time.RFC3339Nano), ProviderProgressAt: started.Format(time.RFC3339Nano), ProcessState: "starting", SandboxHealth: "unknown", SandboxPhase: "unknown", ConfigSnapshotVersion: 1, ConfigSnapshotHash: configHash, ExecutionProfile: a.input.ExecutionProfile, AttemptID: execution.id, Task: a.task.ID, UnitID: a.input.UnitID, OperationID: a.input.OperationID, Purpose: a.input.Purpose, PreviousAttemptID: a.previousAttempt, ResumedFromAttemptID: a.resumeSource, RecoveryEpisodeID: episodeID, RestartCount: a.recoveryRestartCount, QueuePosition: a.queuePosition, QueueLength: a.queueLength, Role: a.input.Role, Provider: chosen.ID, SessionID: a.input.SessionID, RequestedSessionID: a.input.SessionID, CWD: a.input.CWD, PlanVersion: a.task.PlanVersion, Finished: false, Status: "running", Owner: a.owner, ContextCommit: a.index.Commit, ContextIndexVersion: a.index.Version, PolicyHash: a.project.PolicyHash, Model: a.input.Model, ProjectID: a.project.ID, ProviderName: chosen.Name, Transport: provider.ExecutionProvider(chosen, a.input.Thinking).Kind, Thinking: a.input.Thinking, DeadlineAt: deadline.Format(time.RFC3339Nano), StartedAt: started.Format(time.RFC3339Nano)}
	if a.input.Execution != nil {
		execution.state.SandboxName = a.input.Execution.SandboxName
		execution.state.ExecutionLabelValue = a.input.Execution.ExecutionLabelValue
		execution.state.LifecycleFile = a.input.Execution.LifecycleFile
		execution.state.GatewayName = a.input.Execution.GatewayName
		execution.state.GatewayEndpoint = a.input.Execution.GatewayEndpoint
		execution.state.Workspace = a.input.Execution.Workspace
	}
	if err := a.Store.Put(a.ctx, "sessions", execution.id, execution.state, 0); err != nil {
		return err
	}
	if err := execution.captureInput(); err != nil {
		return err
	}
	if err := execution.saveStarted(); err != nil {
		return err
	}
	return nil
}

func (a *agentRun) recordPrelaunchFailure(attempt *agentAttempt, chosen model.Provider, failure error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entry := preflightUsageEntry(a, attempt, chosen)
	changes := a.failedAttemptChanges(ctx, attempt.id, failure)
	return a.Store.CompleteUsage(ctx, entry, changes)
}

func preflightUsageEntry(a *agentRun, attempt *agentAttempt, chosen model.Provider) model.UsageEntry {
	zero := int64(0)
	zeroCost := float64(0)
	return model.UsageEntry{Kind: "preflight", AttemptID: attempt.id, TaskID: a.task.ID, ProjectID: a.project.ID, Provider: chosen.ID, Model: a.input.Model, Role: a.input.Role, Usage: &model.Usage{Scope: "attempt", TotalTokens: &zero, Cost: &zeroCost, Source: "harness_preflight", Completeness: "no_inference_started"}}
}

func (a *agentRun) failedAttemptChanges(ctx context.Context, attemptID string, failure error) []store.Change {
	record, err := a.Store.Get(ctx, "sessions", attemptID)
	if err != nil {
		return nil
	}
	state, err := store.Decode[model.Attempt](record)
	if err != nil {
		return nil
	}
	state.Finished = true
	state.Status = "failed"
	state.Error = failure.Error()
	state.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return []store.Change{{Kind: "sessions", ID: attemptID, Value: state, Expected: record.Version}}
}

func (a *agentRun) prepareAttemptResume(chosen model.Provider) error {
	saved, err := a.findResumeSession(a.ctx, a.task.ID, a.input.Role, chosen.ID, a.input.CWD, a.task.PlanVersion, a.input.Model, provider.ExecutionProvider(chosen, a.input.Thinking).Kind, thinkingKey(a.input.Thinking), a.input.Purpose, a.input.UnitID, a.input.ExecutionProfile)
	if err != nil {
		return fmt.Errorf("%w: session lookup: %w", store.ErrPersistence, err)
	}
	a.resumeSource = saved.AttemptID
	a.input.SessionID = ""
	if saved.Session != "" {
		a.input.SessionID = saved.Session
	}
	return nil
}

func (a *agentAttempt) captureInput() error {
	prompt, err := a.Store.Capture(a.ctx, a.id, "prompt", "text/plain", []byte(a.input.Prompt), store.MessagePayloadLimit, false)
	if err != nil {
		return err
	}
	snapshot, err := store.SafeJSON(model.AttemptInput{ExecutionProfile: a.input.ExecutionProfile, Handoff: a.task.Handoff, Policy: a.project.Policy, Provider: provider.ExecutionProvider(a.chosen, a.input.Thinking), ReadOnly: a.input.ReadOnly, ContextCommit: a.index.Commit, ContextIndexVersion: a.index.Version, Model: a.input.Model, Thinking: a.input.Thinking})
	if err != nil {
		return err
	}
	config, err := a.Store.Capture(a.ctx, a.id, "input_snapshot", "application/json", snapshot, store.MessagePayloadLimit, false)
	if err != nil {
		return err
	}
	a.state.PromptArtifact = prompt.ID
	a.state.InputArtifact = config.ID
	return nil
}

func (a *agentAttempt) saveStarted() error {
	if a.fallbackReason != "" {
		a.event("whirlwind/fallback", map[string]string{"previous_attempt_id": a.previousAttempt, "reason": a.fallbackReason})
		a.fallbackReason = ""
	}
	r, err := a.Store.Get(a.ctx, "sessions", a.id)
	if err != nil {
		return err
	}
	if err = a.startRecoveryEpisode(a.ctx, r); err != nil {
		return err
	}
	a.previousAttempt = a.id
	return a.eventErr
}
